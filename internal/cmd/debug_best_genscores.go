package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/gizmo-platform/gameday/modules/best"
	"github.com/gizmo-platform/gameday/pkg/db"
)

var (
	debugBestGenscoresCmd = &cobra.Command{
		Use:   "genscores",
		Short: "genscores",
		Long:  debugBestGenscoresCmdLongDocs,
		Run:   debugBestGenscoresCmdRun,
	}

	debugBestGenscoresCmdLongDocs = `debug best genscores

Generate scores for every team.  DO NOT RUN THIS AGAINST A LIVE SERVER.  This command will remove all external scores and then refill the database with completely randomized scores for each team, with values clamped to the maximums defined by the module.  This is useful for testing scoreboard information and output data that depends on having scores in the database.
`
)

func init() {
	debugBestCmd.AddCommand(debugBestGenscoresCmd)
}

func debugBestGenscoresCmdRun(c *cobra.Command, args []string) {
	d, err := db.New()
	if err != nil {
		slog.Error("Error initializing database", "error", err)
		os.Exit(2)
	}

	// This only partially initializes this module, which means
	// that we have to be careful what we call below since a lot
	// of things will explode with the module only partially
	// initialized.
	b := best.New(d, nil, nil)

	if err := b.DebugGenerateScores(); err != nil {
		slog.Error("Error generating best scores", "error", err)
		os.Exit(2)
	}
}
