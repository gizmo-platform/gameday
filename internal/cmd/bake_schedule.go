package cmd

import (
	"fmt"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

var (
	bakeScheduleCmd = &cobra.Command{
		Use:   "schedule <rounds> <fields> <positions> <teams>",
		Short: "generate a schedule with the given parameters",
		Run:   bakeScheduleCmdRun,
		Args:  cobra.ExactArgs(4),
	}
)

func init() {
	bakeCmd.AddCommand(bakeScheduleCmd)
}

func bakeScheduleCmdRun(c *cobra.Command, args []string) {

	cfg := schedgen.Config{
		Rounds:    strToInt(args[0]),
		Fields:    strToInt(args[1]),
		Positions: strToInt(args[2]),
		Teams:     strToInt(args[3]),
	}

	s := schedgen.Generate(cfg)

	s.Score()

	tw := table.NewWriter()
	h := []interface{}{"Round", "Match"}
	for field := range strToInt(args[1]) {
		for pos := range strToInt(args[2]) {
			h = append(h, fmt.Sprintf("F%d-P%d", field+1, pos+1))
		}
	}
	tw.AppendHeader(h)
	m := 1
	for r, round := range s.Rounds {
		for _, match := range round.Matches {
			t := []interface{}{r + 1, m}
			for field := range strToInt(args[1]) {
				for pos := range strToInt(args[2]) {
					ft := match.Team(field+1, pos+1)
					if ft > 0 {
						t = append(t, ft)
					}
				}
			}
			tw.AppendRow(table.Row(t))
			m++
		}
	}

	fmt.Println(tw.Render())

	totalScore := s.Score()

	fmt.Println("Schedule Generated")
	fmt.Printf("Team Scores: best=%d, worst=%d, avg=%d\n", s.TeamBestScore, s.TeamWorstScore, s.TeamAvgScore)
	fmt.Printf("High Level: closest-replay=%d, closest-match=%d, worst-pos-diversity=%d, worst-field-diversity=%d\n",
		s.ClosestReplay, s.ClosestReplayMatch, s.WorstPositionDiversity, s.WorstFieldDiversity)
	fmt.Printf("Overall Score: %d\n", totalScore)
}
