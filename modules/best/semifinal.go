package best

import (
	"fmt"

	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

func init() {
	schedgen.RegisterGenerator("Semifinal", NewBESTSemifinal)
	schedgen.RegisterGeneratorConfig("Semifinal", BESTSemifinalConfig{})
}

// Table 1.4.6.3.a — 8-team semifinal field position assignments.
// Column order is Yellow, Blue, Red, Green. Values are 1-based seed
// numbers; the team index bound to seed N is N-1.
var semiFinal8 = [6][4]int{
	{4, 6, 3, 2}, // M1
	{7, 1, 5, 8}, // M2
	{3, 7, 8, 4}, // M3
	{6, 5, 2, 1}, // M4
	{5, 3, 4, 7}, // M5
	{8, 2, 1, 6}, // M6
}

// Table 1.4.6.3.b — 16-team semifinal field position assignments.
// Column order is Yellow, Blue, Red, Green. Values are 1-based seed
// numbers; the team index bound to seed N is N-1.
var semiFinal16 = [12][4]int{
	{4, 13, 1, 16},  // M1
	{5, 10, 3, 15},  // M2
	{6, 9, 8, 11},   // M3
	{16, 4, 2, 14},  // M4
	{8, 5, 6, 12},   // M5
	{7, 11, 9, 10},  // M6
	{3, 14, 13, 2},  // M7
	{10, 12, 5, 1},  // M8
	{15, 6, 16, 7},  // M9
	{14, 8, 11, 13}, // M10
	{1, 7, 4, 3},    // M11
	{2, 15, 12, 9},  // M12
}

// BESTSemifinalConfig provides information on the configuration
// defaults for the BEST semifinal rotation.
type BESTSemifinalConfig struct{}

func (c BESTSemifinalConfig) MaxRounds() int { return 3 }

func (c BESTSemifinalConfig) DefaultRounds() int { return 3 }

func (c BESTSemifinalConfig) RoundsDynamic() bool { return false }

func NewBESTSemifinal(c schedgen.Config) schedgen.Generator {
	return &BESTSemifinalSchedule{
		Schedule: schedgen.Schedule{
			Config:        c,
			ClosestReplay: 99,
		},
	}
}

// BESTSemifinalSchedule produces the fixed BEST semifinal rotation.
// Team index i corresponds to seed i+1: the table is a pure
// lookup from seed to position, and the binding of an actual team
// to an index is the responsibility of the placement layer. The
// rotation may play on multiple fields: consecutive table matches
// are grouped into blocks of Config.Fields and each block is
// emitted as one concurrent match spanning fields 0..Fields-1.
// In division-aware scheduling a 16-team division must be pinned
// to 2 or 3 fields (auto-assignment would request 4), and
// NoCompact divisions forfeit cross-division compaction during
// interleave.
type BESTSemifinalSchedule struct {
	schedgen.Schedule
}

// validFieldCounts returns the field counts for which grouping the
// rotation table's matches into consecutive blocks of that size
// yields team-disjoint blocks (no team double-booked in a slot).
func validFieldCounts(table [][4]int) []int {
	valid := []int{}
	for f := 1; f < len(table); f++ {
		if len(table)%f != 0 {
			continue
		}
		ok := true
	outer:
		for slot := 0; slot < len(table); slot += f {
			seen := make(map[int]struct{}, f*4)
			for _, row := range table[slot : slot+f] {
				for _, seed := range row {
					if _, dup := seen[seed]; dup {
						ok = false
						break outer
					}
					seen[seed] = struct{}{}
				}
			}
		}
		if ok {
			valid = append(valid, f)
		}
	}
	return valid
}

// Generate looks up the fixed semifinal rotation for the configured
// team count and returns it as a single round. Team counts other
// than 8 or 16 are not supported by the rotation tables and return
// an error. When Config.Fields is greater than one, consecutive
// table matches are grouped into blocks of that size and each block
// becomes one concurrent match spanning fields 0..Fields-1; field
// counts whose blocks are not team-disjoint are rejected.
func (s *BESTSemifinalSchedule) Generate() (*schedgen.Schedule, error) {
	var table [][4]int
	switch s.Config.Teams {
	case 8:
		table = semiFinal8[:]
	case 16:
		table = semiFinal16[:]
	default:
		return nil, fmt.Errorf("Semifinal: unsupported team count %d (must be 8 or 16)", s.Config.Teams)
	}

	fields := s.Config.Fields
	if fields < 1 {
		fields = 1
	}
	if len(table)%fields != 0 {
		return nil, fmt.Errorf("Semifinal: %d-team rotation has %d matches, not divisible by %d fields (valid field counts: %v)",
			s.Config.Teams, len(table), fields, validFieldCounts(table))
	}

	r := schedgen.Round{TeamAppearances: make(map[int]int)}
	for slot := 0; slot < len(table); slot += fields {
		seen := make(map[int]int, fields*4)
		for j := 0; j < fields; j++ {
			for _, seed := range table[slot+j] {
				if prev, dup := seen[seed]; dup {
					return nil, fmt.Errorf("Semifinal: %d-team rotation is not valid on %d fields (matches %d and %d share seed %d)",
						s.Config.Teams, fields, slot+prev+1, slot+j+1, seed)
				}
				seen[seed] = j
			}
		}

		placements := make(map[schedgen.Location]int)
		for j := 0; j < fields; j++ {
			for col, seed := range table[slot+j] {
				teamIndex := seed - 1
				placements[schedgen.Location{Field: j, Position: col}] = teamIndex
				if _, done := r.TeamAppearances[teamIndex]; !done {
					r.TeamAppearances[teamIndex] = slot / fields
				}
			}
		}
		r.Matches = append(r.Matches, schedgen.Match{Placements: placements})
	}

	s.Rounds = []schedgen.Round{r}
	return &s.Schedule, nil
}
