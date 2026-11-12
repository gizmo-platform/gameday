package schedgen

import (
	"fmt"
	"log/slog"
)

type teamStats struct {
	LastMatch          int
	ClosestReplay      int
	ClosestReplayMatch int
	ClosestReplayRound int
	PlayedMatches      []int
	PlayedLocations    map[Location]struct{}
	OtherTeams         map[int]struct{}
}

func (ts teamStats) Score() int {
	return ts.ClosestReplay * len(ts.PlayedLocations) * len(ts.OtherTeams)
}

// Score is used to work out how good a schedule is.
func (s *Schedule) Score() int {
	s.ClosestReplay = 99
	s.WorstLocationDiversity = 99
	s.WorstCompetitorDiversity = 99
	s.TeamBestScore = 0
	s.TeamWorstScore = 0
	s.TeamAvgScore = 0

	ts := make(map[int]*teamStats)
	width := s.Config.ActiveWidth()
	for team := range s.Config.Teams {
		ts[team] = &teamStats{
			ClosestReplay:   99,
			PlayedLocations: make(map[Location]struct{}),
			OtherTeams:      make(map[int]struct{}),
		}
	}

	for roundID, round := range s.Rounds {
		for matchNum, match := range round.Matches {
			matchNum = matchNum + roundID*len(round.Matches)
			// Iterate placements directly: with a narrowed match width
			// the physical positions rotate per wave, so seats are not
			// confined to the first width indices.
			fieldTeams := make(map[int][]int)
			for loc, t := range match.Placements {
				fieldTeams[loc.Field] = append(fieldTeams[loc.Field], t)
			}
			for _, t := range match.Placements {
				ts[t].PlayedMatches = append(ts[t].PlayedMatches, matchNum)
			}
			for loc, t := range match.Placements {
				ts[t].PlayedLocations[loc] = struct{}{}
				for _, peer := range fieldTeams[loc.Field] {
					if peer == t {
						continue
					}
					ts[t].OtherTeams[peer] = struct{}{}
				}
			}
		}
	}

	for t := range ts {
		for i := 1; i < len(ts[t].PlayedMatches); i++ {
			delta := (ts[t].PlayedMatches[i] - ts[t].PlayedMatches[i-1])
			if ts[t].ClosestReplay > delta {
				ts[t].ClosestReplay = delta
				ts[t].LastMatch = ts[t].PlayedMatches[i-1]
				ts[t].ClosestReplayMatch = ts[t].PlayedMatches[i]
				ts[t].ClosestReplayRound = i
			}
		}
	}

	for t := range s.Config.Teams {
		tsc := ts[t].Score()

		if t == 0 || tsc > s.TeamBestScore {
			s.TeamBestScore = tsc
		}
		if t == 0 || tsc < s.TeamWorstScore {
			s.TeamWorstScore = tsc
		}
		s.TeamAvgScore += tsc

		if s.ClosestReplay > ts[t].ClosestReplay {
			slog.Debug("New closest replay match",
				"distance", ts[t].ClosestReplay,
				"team", t,
				"match", ts[t].ClosestReplayMatch,
				"last", ts[t].LastMatch,
				"fault-round", ts[t].ClosestReplayRound,
				"played-matches", ts[t].PlayedMatches,
			)
			s.ClosestReplay = ts[t].ClosestReplay
			s.ClosestReplayMatch = ts[t].ClosestReplayMatch
			s.ClosestReplayRound = ts[t].ClosestReplayRound
		}
		if s.WorstLocationDiversity > len(ts[t].PlayedLocations) {
			s.WorstLocationDiversity = len(ts[t].PlayedLocations)
		}

		if s.WorstCompetitorDiversity > len(ts[t].OtherTeams) {
			s.WorstCompetitorDiversity = len(ts[t].OtherTeams)
		}
	}
	total := s.TeamAvgScore
	s.TeamAvgScore = s.TeamAvgScore / s.Config.Teams

	return total / (s.Config.Fields * width)
}

func (s *Schedule) Validate() error {
	teamMatches := make(map[int][]int)

	for roundID, round := range s.Rounds {
		for matchNum, match := range round.Matches {
			matchNum = matchNum + roundID*len(round.Matches)
			for _, t := range match.Placements {
				teamMatches[t] = append(teamMatches[t], matchNum)
			}
		}
	}

	for team := range teamMatches {
		if !s.RoundsDynamic && !s.Interleaved && len(teamMatches[team]) != s.Config.Rounds {
			slog.Error("Team does not play expected number of rounds",
				"team", team,
				"expected", s.Config.Rounds,
				"actual", len(teamMatches[team]),
			)
			return InvariantViolation{msg: "team does not play expected number of rounds"}
		}
	}

	for i := range s.Config.Teams {
		if _, found := teamMatches[i]; !found {
			return InvariantViolation{msg: fmt.Sprintf("team %d does not appear in schedule", i)}
		}
	}
	return nil
}
