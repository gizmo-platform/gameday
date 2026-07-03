package schedgen

import (
	"log/slog"
	"math/rand"
)

func init() {
	RegisterGenerator("RandomSeeding", NewRandom)
}

func NewRandom(c Config) Generator {
	r := RandomSchedule{
		Schedule: Schedule{
			Config:        c,
			ClosestReplay: 99,
		},
	}
	return &r
}

// RandomSchedule contains the collection of matches in order.
type RandomSchedule struct {
	Schedule
}

// Generate generates a candidate schedule.  The schedule is
// pre-scored on generation.
func (rs *RandomSchedule) Generate() *Schedule {
	// Pre-generate the zeroth round since all schedules must
	// contain at least one round.
	rs.Rounds = []Round{rs.GenerateRound()}

	// PositionsPlayed needs to be pre-seeded for the 0th round,
	// and then future rounds check to see if additional positions
	// are being played.
	positionsPlayed := make(map[int]map[Location]struct{})
	for team, loc := range rs.Rounds[0].PositionsPlayedByTeam() {
		positionsPlayed[team] = make(map[Location]struct{})
		positionsPlayed[team][loc] = struct{}{}
	}

	// Walk through remaining rounds, optimizing for distance
	// between consecutive appearances for the same team.  This is
	// measured in schedule delta between two matches by number
	// only, to account for inconsistent match timing and breaks
	// that the schedule generator can't know about.
	for round := range rs.Config.Rounds - 1 {
		round++

		r := rs.GenerateRound()
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
			for t := range rs.Config.Teams {
				last := rs.Rounds[round-1].GetRelativeMatchForTeam(t)
				this := r.GetRelativeMatchForTeam(t)
				downtime[t] = this + len(rs.Rounds[round-1].Matches) - last
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
			r = rs.GenerateRound()
		}
		for team, loc := range bestRound.PositionsPlayedByTeam() {
			positionsPlayed[team][loc] = struct{}{}
		}

		rs.Rounds = append(rs.Rounds, bestRound)
	}

	return &rs.Schedule
}

func (s *RandomSchedule) GenerateRound() Round {
	r := Round{}
	r.TeamAppearances = make(map[int]int)

	slots := []Location{}
	for f := range s.Config.Fields {
		for p := range s.Config.Positions {
			slots = append(slots, Location{f, p})
		}
	}

	teams := rand.Perm(s.Config.Teams)
	match := 0
	placements := make(map[Location]int)
	for i := range s.Config.Teams {
		slot := slots[i%len(slots)]
		placements[slot] = teams[i]
		r.TeamAppearances[teams[i]] = match

		if i%len(slots) == len(slots)-1 {
			slog.Debug("Placements", "map", placements)
			r.Matches = append(r.Matches, Match{Placements: placements})
			placements = make(map[Location]int)
			match++
		}
	}
	if len(placements) > 0 {
		// This commits the leftover partial match that can
		// exist when the number of teams doesn't cleanly
		// divide into the available slots.
		r.Matches = append(r.Matches, Match{Placements: placements})
	}

	if len(r.TeamAppearances) != s.Config.Teams {
		slog.Error("SCHEDULER INVARIANT VIOLATION", "expected-count", s.Config.Teams, "actual-count", len(r.TeamAppearances))
	}

	seen := make(map[int]struct{})
	for _, m := range r.Matches {
		for _, t := range m.Placements {
			seen[t] = struct{}{}
		}
	}
	for t := range s.Config.Teams {
		if _, found := seen[t]; !found {
			slog.Error("Team does not appear in round!", "team", t)
		}
	}

	return r
}
