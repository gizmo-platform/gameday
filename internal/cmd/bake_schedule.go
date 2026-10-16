package cmd

import (
	"fmt"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

var (
	bakeScheduleCmd = &cobra.Command{
		Use:   "schedule <scheduler> <rounds> <fields> <positions> <teams>",
		Short: "generate a schedule with the given parameters",
		Run:   bakeScheduleCmdRun,
		Args:  cobra.ExactArgs(5),
	}
)

func init() {
	bakeCmd.AddCommand(bakeScheduleCmd)
}

func bakeScheduleCmdRun(c *cobra.Command, args []string) {
	scheduler := args[0]
	cfg := schedgen.Config{
		Rounds:    strToInt(args[1]),
		Fields:    strToInt(args[2]),
		Positions: strToInt(args[3]),
		Teams:     strToInt(args[4]),
	}

	s, err := schedgen.GenerateSchedule(scheduler, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not generate schedule: %s\n", err)
		return
	}
	s.Score()

	tw := table.NewWriter()
	h := []interface{}{"Round", "Match"}
	for field := range cfg.Fields {
		for pos := range cfg.Positions {
			h = append(h, schedgen.Location{Field: field, Position: pos}.String())
		}
	}
	tw.AppendHeader(h)
	m := 1
	for r, round := range s.Rounds {
		for _, match := range round.Matches {
			t := []interface{}{r + 1, m}
			for field := range cfg.Fields {
				for pos := range cfg.Positions {
					// An empty position prints a blank cell so later
					// teams stay under their own column.
					if ft := match.Team(field, pos); ft >= 0 {
						t = append(t, ft)
					} else {
						t = append(t, "")
					}
				}
			}
			tw.AppendRow(table.Row(t))
			m++
		}
	}

	fmt.Println(tw.Render())

	if err := s.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error validating schedule %v\n", err)
		return
	}

	totalScore := s.Score()

	fmt.Println("Schedule Generated")
	fmt.Printf("Team Scores: best=%d, worst=%d, avg=%d\n", s.TeamBestScore, s.TeamWorstScore, s.TeamAvgScore)
	fmt.Printf("High Level: closest-replay=%d, closest-match=%d, worst-location-diversity=%d worst-competitor-diversity=%d\n",
		s.ClosestReplay, s.ClosestReplayMatch, s.WorstLocationDiversity, s.WorstCompetitorDiversity)
	fmt.Printf("Overall Score: %d\n", totalScore)
}
