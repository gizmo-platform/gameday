package schedgen

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

//go:embed all:baked
var bakedFS embed.FS

// bakedDir is the directory, inside the embedded filesystem, that
// holds baked schedule artifacts. The bake command writes its output
// here so the next build ships the schedules.
const bakedDir = "baked"

// BakedSchedule is a pre-computed schedule design stored as a YAML
// artifact and embedded into the binary. All schedule metrics are
// invariant under relabeling of teams, so one baked design serves
// every event that shares its shape: at runtime the design is
// truncated to the requested round count and given a fresh random
// team labeling.
//
// Grid rows are rounds; each row holds one entry per slot of the
// padded shape (waves x fields x positions) in wave-major,
// field-major, position-minor order. Entries are zero-based team
// indexes; -1 marks an empty slot in the final wave of a schedule
// whose team count doesn't divide evenly.
type BakedSchedule struct {
	// Shape.
	Teams     int `yaml:"teams"`
	Fields    int `yaml:"fields"`
	Positions int `yaml:"positions"`

	// Rounds is the number of round rows in Grid, i.e. the round cap
	// this design can serve.
	Rounds int `yaml:"rounds"`

	// Search metadata, recorded for reviewability.
	Iterations int   `yaml:"iterations,omitempty"`
	Restarts   int   `yaml:"restarts,omitempty"`
	Seed       int64 `yaml:"seed,omitempty"`
	Cost       int   `yaml:"cost,omitempty"`

	Grid [][]int `yaml:"grid"`
}

// BakedFileName returns the canonical artifact name for a shape.
func BakedFileName(teams, fields, positions, rounds int) string {
	return fmt.Sprintf("teams%d-fields%d-positions%d-rounds%d.yml",
		teams, fields, positions, rounds)
}

// FileName is the artifact name this schedule would be stored under.
func (b *BakedSchedule) FileName() string {
	return BakedFileName(b.Teams, b.Fields, b.Positions, b.Rounds)
}

// Validate checks the artifact's internal consistency: the grid has
// the advertised shape and each round is a permutation of the teams
// plus the correct number of empty slots.
func (b *BakedSchedule) Validate() error {
	slots := b.Fields * b.Positions
	if b.Teams < 1 || slots < 1 {
		return errors.New("baked schedule has no teams or slots")
	}
	waves := (b.Teams + slots - 1) / slots
	perRound := waves * slots
	if b.Rounds != len(b.Grid) {
		return fmt.Errorf("rounds %d does not match grid rows %d", b.Rounds, len(b.Grid))
	}
	seen := make([]bool, b.Teams)
	for r, row := range b.Grid {
		if len(row) != perRound {
			return fmt.Errorf("round %d has %d slots, expected %d", r, len(row), perRound)
		}
		clear(seen)
		for _, t := range row {
			if t == -1 {
				continue
			}
			if t < 0 || t >= b.Teams {
				return fmt.Errorf("round %d references team %d outside 0..%d", r, t, b.Teams-1)
			}
			if seen[t] {
				return fmt.Errorf("round %d plays team %d twice", r, t)
			}
			seen[t] = true
		}
	}
	return nil
}

// ParseBaked decodes a baked schedule artifact.
func ParseBaked(data []byte) (*BakedSchedule, error) {
	var b BakedSchedule
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parsing baked schedule: %w", err)
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return &b, nil
}

// Marshal renders the artifact as YAML with one grid row per line,
// which keeps the schedule readable and diffs compact while remaining
// plain YAML.
func (b *BakedSchedule) Marshal() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	meta := struct {
		Teams      int   `yaml:"teams"`
		Fields     int   `yaml:"fields"`
		Positions  int   `yaml:"positions"`
		Rounds     int   `yaml:"rounds"`
		Iterations int   `yaml:"iterations,omitempty"`
		Restarts   int   `yaml:"restarts,omitempty"`
		Seed       int64 `yaml:"seed,omitempty"`
		Cost       int   `yaml:"cost,omitempty"`
	}{b.Teams, b.Fields, b.Positions, b.Rounds, b.Iterations, b.Restarts, b.Seed, b.Cost}

	out, err := yaml.Marshal(meta)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(out)
	fmt.Fprintf(buf, "grid:\n")
	for _, row := range b.Grid {
		cells := make([]string, len(row))
		for i, t := range row {
			cells[i] = strconv.Itoa(t)
		}
		fmt.Fprintf(buf, "  - [%s]\n", strings.Join(cells, ", "))
	}
	return buf.Bytes(), nil
}

// LookupBaked finds the best baked artifact covering a request: an
// exact match on teams, fields, and positions with at least the
// requested number of rounds. When several artifacts cover the
// request the one with the fewest surplus rounds wins, so truncation
// discards as little design as possible. A missing archive or a shape
// that was never baked yields a nil schedule and no error.
func LookupBaked(c Config) (*BakedSchedule, error) {
	entries, err := fs.ReadDir(bakedFS, bakedDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading baked schedule archive: %w", err)
	}

	var best *BakedSchedule
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		teams, fields, positions, rounds, ok := parseBakedFileName(entry.Name())
		if !ok {
			continue
		}
		if teams != c.Teams || fields != c.Fields || positions != c.Positions {
			continue
		}
		if rounds < c.Rounds {
			continue
		}
		if best != nil && rounds >= best.Rounds {
			continue
		}
		data, err := bakedFS.ReadFile(path.Join(bakedDir, entry.Name()))
		if err != nil {
			slog.Warn("Could not read baked schedule", "file", entry.Name(), "error", err)
			continue
		}
		candidate, err := ParseBaked(data)
		if err != nil {
			slog.Warn("Ignoring invalid baked schedule", "file", entry.Name(), "error", err)
			continue
		}
		best = candidate
	}
	return best, nil
}

// parseBakedFileName recovers the shape from a canonical artifact
// name. Names that don't match the convention are skipped.
func parseBakedFileName(name string) (teams, fields, positions, rounds int, ok bool) {
	const (
		prefix = "teams"
		mid1   = "-fields"
		mid2   = "-positions"
		mid3   = "-rounds"
	)
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".yml") {
		return 0, 0, 0, 0, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".yml")

	i := strings.Index(body, mid1)
	if i < 0 {
		return 0, 0, 0, 0, false
	}
	t, err := strconv.Atoi(body[:i])
	if err != nil {
		return 0, 0, 0, 0, false
	}
	rest := body[i+len(mid1):]

	i = strings.Index(rest, mid2)
	if i < 0 {
		return 0, 0, 0, 0, false
	}
	f, err := strconv.Atoi(rest[:i])
	if err != nil {
		return 0, 0, 0, 0, false
	}
	rest = rest[i+len(mid2):]

	i = strings.Index(rest, mid3)
	if i < 0 {
		return 0, 0, 0, 0, false
	}
	p, err := strconv.Atoi(rest[:i])
	if err != nil {
		return 0, 0, 0, 0, false
	}
	r, err := strconv.Atoi(rest[i+len(mid3):])
	if err != nil {
		return 0, 0, 0, 0, false
	}
	return t, f, p, r, true
}

// Materialize turns the baked design into a live Schedule for the
// requested config: the design is truncated to the requested round
// count (never padded beyond it) and every team is given a fresh
// random label. Truncation is uniform — each team plays exactly once
// per round — so dropping rounds keeps the round cap exact and can
// only widen replay gaps.
//
// A non-zero seed reproduces the labeling exactly; a zero seed means
// "no preference" and draws a fresh labeling seed per call, so two
// events of the same shape never receive identical team assignments.
func (b *BakedSchedule) Materialize(c Config, seed int64) (*Schedule, error) {
	if c.Teams != b.Teams || c.Fields != b.Fields || c.Positions != b.Positions {
		return nil, fmt.Errorf("baked schedule shape %dx%dx%d does not match request %dx%dx%d",
			b.Teams, b.Fields, b.Positions, c.Teams, c.Fields, c.Positions)
	}
	if c.Rounds < 1 {
		return nil, errors.New("requested round count must be at least 1")
	}
	if c.Rounds > b.Rounds {
		return nil, fmt.Errorf("baked schedule covers %d rounds, request needs %d",
			b.Rounds, c.Rounds)
	}

	if seed == 0 {
		// No preference given: draw a fresh labeling seed so
		// repeated requests for the same shape do not all get
		// the identical team assignment.
		seed = rand.Int63()
	}
	rnd := rand.New(rand.NewSource(seed))
	label := rnd.Perm(b.Teams)

	// buildRounds treats entities at or above Teams as empty slots.
	perm := make([][]int, c.Rounds)
	for r := 0; r < c.Rounds; r++ {
		row := b.Grid[r]
		perm[r] = make([]int, len(row))
		for s, t := range row {
			if t == -1 {
				perm[r][s] = b.Teams
				continue
			}
			perm[r][s] = label[t]
		}
	}

	s := &Schedule{
		Config:        c,
		Rounds:        buildRounds(c, perm),
		ClosestReplay: 99,
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("materialized baked schedule failed validation: %w", err)
	}
	return s, nil
}

// BakedShapes lists the shapes covered by the embedded archive,
// sorted by teams, fields, positions, then rounds.
func BakedShapes() ([]BakedShape, error) {
	return bakedShapesIn(bakedFS, bakedDir)
}

// BakedShapesInDir lists the shapes covered by an on-disk directory of
// artifacts. The bake command uses it to report what --out already
// holds.
func BakedShapesInDir(dir string) ([]BakedShape, error) {
	return bakedShapesIn(os.DirFS(dir), ".")
}

// sortedShapes orders a shape slice by teams, fields, positions, rounds.
func sortedShapes(shapes []BakedShape) []BakedShape {
	sort.Slice(shapes, func(i, j int) bool {
		a, b := shapes[i], shapes[j]
		switch {
		case a.Teams != b.Teams:
			return a.Teams < b.Teams
		case a.Fields != b.Fields:
			return a.Fields < b.Fields
		case a.Positions != b.Positions:
			return a.Positions < b.Positions
		default:
			return a.Rounds < b.Rounds
		}
	})
	return shapes
}

func bakedShapesIn(fsys fs.FS, dir string) ([]BakedShape, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading baked schedule archive: %w", err)
	}
	shapes := []BakedShape{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		teams, fields, positions, rounds, ok := parseBakedFileName(entry.Name())
		if !ok {
			continue
		}
		shapes = append(shapes, BakedShape{Teams: teams, Fields: fields, Positions: positions, Rounds: rounds})
	}
	return sortedShapes(shapes), nil
}

// BakedShape is one (teams, fields, positions, rounds) tuple covered
// by the baked archive.
type BakedShape struct {
	Teams     int
	Fields    int
	Positions int
	Rounds    int
}

// BakeSchedule generates a schedule and packages it as an embeddable
// artifact. It always runs the search, ignoring any existing baked
// archive for the shape.
func BakeSchedule(c Config, opts AnnealOptions) (*BakedSchedule, error) {
	opts = opts.withDefaults(c)
	s, e, err := annealBest(c, opts)
	if err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("annealed schedule failed validation: %w", err)
	}

	slots := c.Fields * c.Positions
	waves := (c.Teams + slots - 1) / slots
	perRound := waves * slots

	b := &BakedSchedule{
		Teams:      c.Teams,
		Fields:     c.Fields,
		Positions:  c.Positions,
		Rounds:     len(s.Rounds),
		Iterations: opts.Iterations,
		Restarts:   opts.Restarts,
		Seed:       opts.Seed,
		Cost:       e,
		Grid:       make([][]int, 0, len(s.Rounds)),
	}
	for _, round := range s.Rounds {
		row := make([]int, perRound)
		for i := range row {
			row[i] = -1
		}
		for w, m := range round.Matches {
			for f := 0; f < c.Fields; f++ {
				for p := 0; p < c.Positions; p++ {
					t := m.Team(f, p)
					if t < 0 {
						continue
					}
					row[w*slots+slotIndexOf(f, p, c.Positions)] = t
				}
			}
		}
		b.Grid = append(b.Grid, row)
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return b, nil
}

// slotIndexOf maps a (field, position) pair to its index within a
// wave, matching the ordering buildRounds expects.
func slotIndexOf(field, position, positions int) int {
	return field*positions + position
}
