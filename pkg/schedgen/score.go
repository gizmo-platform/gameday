package schedgen

import (
	"log/slog"
)

type teamStats struct {
	LastMatch          int
	ClosestReplay      int
	ClosestReplayMatch int
	ClosestReplayRound int
	PlayedMatches      []int
	Fields             map[int]struct{}
	Positions          map[int]struct{}
	OtherTeams         map[int]struct{}
}

func (ts teamStats) Score() int {
	return ts.ClosestReplay * len(ts.Fields) * len(ts.Positions) * len(ts.OtherTeams)
}

// Score is used to work out how good a schedule is.
func (s *Schedule) Score() int {
	s.ClosestReplay = 99
	s.WorstPositionDiversity = 99
	s.WorstFieldDiversity = 99

	ts := make(map[int]*teamStats)
	for team := range s.Config.Teams {
		ts[team+1] = &teamStats{
			ClosestReplay: 99,
			Fields:        make(map[int]struct{}),
			Positions:     make(map[int]struct{}),
			OtherTeams:    make(map[int]struct{}),
		}
	}

	m := 1
	for roundID, round := range s.Rounds {
		for matchNum, match := range round.Matches {
			matchNum = matchNum + roundID*len(round.Matches)
			for field := range s.Config.Fields {
				for pos := range s.Config.Positions {
					t := match.Team(field+1, pos+1)
					if t == -1 {
						continue
					}
					ts[t].PlayedMatches = append(ts[t].PlayedMatches, matchNum)
					ts[t].Fields[field] = struct{}{}
					ts[t].Positions[pos] = struct{}{}
					for _, peer := range match.PeerTeams(field+1, pos+1, s.Config.Positions) {
						ts[t].OtherTeams[peer] = struct{}{}
					}
				}
			}
			m++
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
		t = t + 1
		tsc := ts[t].Score()

		if tsc > s.TeamBestScore {
			s.TeamBestScore = tsc
		}
		if tsc < s.TeamBestScore {
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
		if s.WorstPositionDiversity > len(ts[t].Positions) {
			s.WorstPositionDiversity = len(ts[t].Positions)
		}
		if s.WorstFieldDiversity > len(ts[t].Fields) {
			s.WorstFieldDiversity = len(ts[t].Fields)
		}
	}
	total := s.TeamAvgScore
	s.TeamAvgScore = s.TeamAvgScore / s.Config.Teams

	return total / (s.Config.Fields * s.Config.Positions)
}
