package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/spf13/cobra"

	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

var (
	bakeSchedulesCmd = &cobra.Command{
		Use:   "schedules",
		Short: "anneal schedule designs over ranges of event sizes and write embeddable artifacts",
		Long:  bakeSchedulesLongDocs,
		Args:  cobra.NoArgs,
		RunE:  bakeSchedulesCmdRun,
	}

	bakeSchedulesLongDocs = `bake schedules

Anneals schedule designs for a cross product of event sizes and writes
each one as a YAML artifact that is embedded into the binary. The
runtime "Annealed" generator loads these artifacts instead of searching,
so events start instantly with a schedule quality that would otherwise
cost minutes of compute per request.

Event sizes are given as ranges, making it convenient to bake whole
families of events in one run. A range is a comma-separated list of
values and inclusive spans, e.g. "8-12,16,24-40:4" means 8, 9, 10, 11,
12, 16, 24, 28, 32, 36, 40.

Artifacts already present are skipped unless --force is given, so a
large bake can be interrupted and resumed.`

	bakeSchedulesFlags struct {
		teams      string
		fields     string
		positions  string
		rounds     string
		out        string
		iterations int
		restarts   int
		seed       int64
		workers    int
		force      bool
		list       bool
		dryRun     bool
	}
)

func init() {
	fs := bakeSchedulesCmd.Flags()
	fs.StringVar(&bakeSchedulesFlags.teams, "teams", "", "Team counts, e.g. \"8-40:2\" (required unless --list)")
	fs.StringVar(&bakeSchedulesFlags.fields, "fields", "1", "Field counts, e.g. \"1-4\"")
	fs.StringVar(&bakeSchedulesFlags.positions, "positions", "2", "Position counts, e.g. \"2,3,4\"")
	fs.StringVar(&bakeSchedulesFlags.rounds, "rounds", "5", "Round caps, e.g. \"3-10\"")
	fs.StringVarP(&bakeSchedulesFlags.out, "out", "o", "pkg/schedgen/baked", "Directory to write artifacts into")
	fs.IntVar(&bakeSchedulesFlags.iterations, "iterations", 0, "Moves per search (0 = the bake default, near convergence)")
	fs.IntVar(&bakeSchedulesFlags.restarts, "restarts", 1, "Independent searches per shape, best result wins")
	fs.Int64Var(&bakeSchedulesFlags.seed, "seed", 0x5EED, "Base RNG seed; fixes the produced designs")
	fs.IntVarP(&bakeSchedulesFlags.workers, "workers", "w", 1, "Shapes to anneal concurrently")
	fs.BoolVar(&bakeSchedulesFlags.force, "force", false, "Re-bake artifacts that already exist")
	fs.BoolVar(&bakeSchedulesFlags.list, "list", false, "List the shapes covered by --out and exit")
	fs.BoolVar(&bakeSchedulesFlags.dryRun, "dry-run", false, "Print the shapes that would be baked without computing")

	bakeCmd.AddCommand(bakeSchedulesCmd)
}

func bakeSchedulesCmdRun(c *cobra.Command, _ []string) error {
	flags := &bakeSchedulesFlags

	if flags.list {
		return listBakedShapes(flags.out)
	}

	if flags.teams == "" {
		return errors.New("--teams is required (see --help for the range syntax)")
	}

	teamList, err := parseIntRange(flags.teams)
	if err != nil {
		return fmt.Errorf("--teams: %w", err)
	}
	fieldList, err := parseIntRange(flags.fields)
	if err != nil {
		return fmt.Errorf("--fields: %w", err)
	}
	positionList, err := parseIntRange(flags.positions)
	if err != nil {
		return fmt.Errorf("--positions: %w", err)
	}
	roundList, err := parseIntRange(flags.rounds)
	if err != nil {
		return fmt.Errorf("--rounds: %w", err)
	}

	type shape struct{ teams, fields, positions, rounds int }
	shapes := []shape{}
	for _, teams := range teamList {
		for _, fields := range fieldList {
			for _, positions := range positionList {
				for _, rounds := range roundList {
					shapes = append(shapes, shape{teams, fields, positions, rounds})
				}
			}
		}
	}
	sort.Slice(shapes, func(i, j int) bool {
		a, b := shapes[i], shapes[j]
		switch {
		case a.teams != b.teams:
			return a.teams < b.teams
		case a.fields != b.fields:
			return a.fields < b.fields
		case a.positions != b.positions:
			return a.positions < b.positions
		default:
			return a.rounds < b.rounds
		}
	})

	if !flags.force {
		kept := shapes[:0]
		for _, s := range shapes {
			name := schedgen.BakedFileName(s.teams, s.fields, s.positions, s.rounds)
			if _, err := os.Stat(filepath.Join(flags.out, name)); err == nil {
				continue
			}
			kept = append(kept, s)
		}
		if skipped := len(shapes) - len(kept); skipped > 0 {
			fmt.Printf("Skipping %d shape(s) that are already baked (use --force to re-bake)\n", skipped)
		}
		shapes = kept
	}

	if flags.dryRun {
		for _, s := range shapes {
			fmt.Printf("%s\n", schedgen.BakedFileName(s.teams, s.fields, s.positions, s.rounds))
		}
		fmt.Printf("%d shape(s) would be baked\n", len(shapes))
		return nil
	}

	if err := os.MkdirAll(flags.out, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	workers := flags.workers
	if workers < 1 {
		workers = 1
	}
	fmt.Printf("Baking %d schedule shape(s) with %d worker(s) into %s\n", len(shapes), workers, flags.out)

	jobs := make(chan shape)
	var (
		wg        sync.WaitGroup
		written   atomic.Int64
		failed    atomic.Int64
		reportLog sync.Mutex
	)

	report := func(format string, args ...any) {
		reportLog.Lock()
		defer reportLog.Unlock()
		fmt.Printf(format, args...)
	}

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				name := schedgen.BakedFileName(s.teams, s.fields, s.positions, s.rounds)
				cfg := schedgen.Config{
					Teams:     s.teams,
					Fields:    s.fields,
					Positions: s.positions,
					Rounds:    s.rounds,
				}
				opts := schedgen.BakeOptions()
				opts.Restarts = flags.restarts
				opts.Seed = flags.seed
				if flags.iterations > 0 {
					opts.Iterations = flags.iterations
				}
				baked, err := schedgen.BakeSchedule(cfg, opts)
				if err != nil {
					failed.Add(1)
					report("FAILED %s: %v\n", name, err)
					continue
				}
				data, err := baked.Marshal()
				if err != nil {
					failed.Add(1)
					report("FAILED %s: %v\n", name, err)
					continue
				}
				path := filepath.Join(flags.out, baked.FileName())
				if err := os.WriteFile(path, data, 0o644); err != nil {
					failed.Add(1)
					report("FAILED %s: %v\n", path, err)
					continue
				}
				n := written.Add(1)
				// Materialize and score the artifact exactly as the
				// runtime would, so the report reflects what an event
				// actually gets and the artifact is validated on bake.
				live, err := baked.Materialize(cfg, baked.Seed)
				if err != nil {
					failed.Add(1)
					report("FAILED %s: %v\n", name, err)
					continue
				}
				live.Score()
				report("[%d/%d] %s cost=%d closest-replay=%d competitor-diversity=%d location-diversity=%d\n",
					n, len(shapes), name, baked.Cost,
					live.ClosestReplay, live.WorstCompetitorDiversity, live.WorstLocationDiversity)
			}
		}()
	}

	for _, s := range shapes {
		jobs <- s
	}
	close(jobs)
	wg.Wait()

	fmt.Printf("Done: %d baked, %d failed\n", written.Load(), failed.Load())
	if failed.Load() > 0 {
		return errors.New("some shapes failed to bake")
	}
	return nil
}

func listBakedShapes(dir string) error {
	shapes, err := schedgen.BakedShapesInDir(dir)
	if err != nil {
		return err
	}
	if len(shapes) == 0 {
		fmt.Printf("No baked schedules found in %s\n", dir)
		return nil
	}
	fmt.Printf("%d baked schedule shape(s) in %s:\n", len(shapes), dir)
	for _, s := range shapes {
		fmt.Printf("  teams=%d fields=%d positions=%d rounds<=%d\n",
			s.Teams, s.Fields, s.Positions, s.Rounds)
	}
	return nil
}

// parseIntRange parses a comma-separated list of integers and inclusive
// spans, where a span may carry a step: "8-12,16,24-40:4".
func parseIntRange(spec string) ([]int, error) {
	out := []int{}
	seen := map[int]bool{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values, err := parseRangePart(part)
		if err != nil {
			return nil, err
		}
		for _, v := range values {
			if v < 1 {
				return nil, fmt.Errorf("value %q must be at least 1", part)
			}
			if seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no values given")
	}
	sort.Ints(out)
	return out, nil
}

func parseRangePart(part string) ([]int, error) {
	lo, hi := part, ""
	step := 1
	if i := strings.Index(part, "-"); i > 0 {
		lo, hi = part[:i], part[i+1:]
		if i := strings.Index(hi, ":"); i >= 0 {
			var err error
			step, err = strconv.Atoi(hi[i+1:])
			if err != nil {
				return nil, fmt.Errorf("bad step in %q: %w", part, err)
			}
			if step < 1 {
				return nil, fmt.Errorf("step must be at least 1 in %q", part)
			}
			hi = hi[:i]
		}
	}

	low, err := strconv.Atoi(strings.TrimSpace(lo))
	if err != nil {
		return nil, fmt.Errorf("bad value %q", part)
	}
	if hi == "" {
		return []int{low}, nil
	}
	high, err := strconv.Atoi(strings.TrimSpace(hi))
	if err != nil {
		return nil, fmt.Errorf("bad range end in %q", part)
	}
	if high < low {
		return nil, fmt.Errorf("range %q counts downwards", part)
	}
	values := []int{}
	for v := low; v <= high; v += step {
		values = append(values, v)
	}
	return values, nil
}
