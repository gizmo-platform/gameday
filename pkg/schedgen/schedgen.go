package schedgen

import (
	"fmt"
	"log/slog"
	"math/rand"
)

const (
	TypeRandomSeeding = "RandomSeeding"
	TypeSemifinal     = "Semifinal"
	TypeFinal         = "Final"
)

// Config sets up the schedule that should be generated.
type Config struct {
	Fields    int
	Positions int
	Teams     int
	Rounds    int
}

// Schedule contains the collection of matches in order.
type Schedule struct {
	Config Config
	Rounds []Round

	TeamBestScore  int
	TeamWorstScore int
	TeamAvgScore   int

	ClosestReplay      int
	ClosestReplayMatch int
	ClosestReplayRound int

	WorstLocationDiversity   int
	WorstCompetitorDiversity int
}

// Round keeps track of the matches in a given round.
type Round struct {
	Matches         []Match
	TeamAppearances map[int]int
}

// GetRelativeMatchForTeam returns the match, counting from zero, that
// a team appears in during a round.
func (r Round) GetRelativeMatchForTeam(t int) int {
	return r.TeamAppearances[t]
}

// PositionsPlayedByTeam is used in working out whether or not a round
// improves position diversity.
func (r Round) PositionsPlayedByTeam() map[int]string {
	out := make(map[int]string)
	for _, m := range r.Matches {
		for loc, team := range m.Placements {
			out[team] = loc
		}
	}
	return out
}

// Match contains a single match.
type Match struct {
	Placements map[string]int
}

func (m Match) Team(field, position int) int {
	t, ok := m.Placements[fmt.Sprintf("%d-%d", field, position)]
	if !ok {
		return -1
	}
	return t + 1
}

func (m Match) PeerTeams(field, position, maxPositions int) []int {
	ret := []int{}
	for pos := range maxPositions {
		if pos+1 == position {
			continue
		}
		ret = append(ret, m.Team(field, pos+1))
	}

	return ret
}

// Generate generates a candidate schedule.  The schedule is
// pre-scored on generation.
func Generate(c Config) *Schedule {
	s := Schedule{
		Config: c,

		ClosestReplay: 99,
	}

	// Pre-generate the zeroth round since all schedules must
	// contain at least one round.
	s.Rounds = []Round{s.GenerateRound()}

	// PositionsPlayed needs to be pre-seeded for the 0th round,
	// and then future rounds check to see if additional positions
	// are being played.
	positionsPlayed := make(map[int]map[string]struct{})
	for team, loc := range s.Rounds[0].PositionsPlayedByTeam() {
		positionsPlayed[team] = make(map[string]struct{})
		positionsPlayed[team][loc] = struct{}{}
	}

	// Walk through remaining rounds, optimizing for distance
	// between consecutive appearances for the same team.  This is
	// measured in schedule delta between two matches by number
	// only, to account for inconsistent match timing and breaks
	// that the schedule generator can't know about.
	for round := range c.Rounds - 1 {
		round++

		r := s.GenerateRound()
		bestRound := r
		bestDowntime := 0
		downtime := make(map[int]int)
		bestPositionScore := 0
		for range 1000 {
			positionScore := 0
			worstDowntime := 99

			// Downtime is how many matches a team has down
			// between the appearance in the previous match and
			// this one.
			for t := range s.Config.Teams {
				last := s.Rounds[round-1].GetRelativeMatchForTeam(t)
				this := r.GetRelativeMatchForTeam(t)
				downtime[t] = this + len(s.Rounds[round-1].Matches) - last
				if downtime[t] < 2 {
					slog.Debug("Evaluating downtime for team", "round", round+1, "team", t+1, "downtime", downtime[t], "this", this, "last", last)
				}
			}

			// Find the worst downtime of the entire
			// round, which should be the closest match
			// that gets replayed.
			for _, d := range downtime {
				if worstDowntime > d {
					worstDowntime = d
				}
			}

			for team, loc := range r.PositionsPlayedByTeam() {
				if _, played := positionsPlayed[team][loc]; !played {
					positionScore++
				}
			}

			if (worstDowntime > bestDowntime) && (bestPositionScore < positionScore) {
				slog.Warn("Replacing best round",
					"old-downtime", bestDowntime,
					"new-downtime", worstDowntime,
					"old-position", bestPositionScore,
					"new-position", positionScore,
				)
				bestDowntime = worstDowntime
				bestPositionScore = positionScore
				bestRound = r
			}
			r = s.GenerateRound()
		}
		for team, loc := range bestRound.PositionsPlayedByTeam() {
			positionsPlayed[team][loc] = struct{}{}
		}

		s.Rounds = append(s.Rounds, bestRound)
	}

	return &s
}

// GenerateRound generates a single potential round and returns it for
// evaluation to determine if the schedule should accept it.
func (s *Schedule) GenerateRound() Round {
	r := Round{}
	m := 0
	placements := make(map[string]int)
	appearances := make(map[int]int)

	f := 1
	p := 1
	for i, t := range rand.Perm(s.Config.Teams) {
		slog.Debug("Placed Team",
			"field", f,
			"position", p,
			"team", t+1,
		)
		placements[fmt.Sprintf("%d-%d", f, p)] = t
		appearances[t] = m
		p++

		// Something in here is wrong.  its not saving rounds that are partially filled.
		if p > s.Config.Positions && f < s.Config.Fields {
			p = 1
			f++
		} else if (p > s.Config.Positions && f >= s.Config.Fields) || i == s.Config.Teams-1 {
			f = 1
			p = 1
			r.Matches = append(r.Matches, Match{Placements: placements})
			placements = make(map[string]int)
			m++
		}
	}

	r.TeamAppearances = appearances
	return r

}
