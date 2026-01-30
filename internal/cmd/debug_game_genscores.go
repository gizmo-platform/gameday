package cmd

import (
	"log/slog"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/pkg/db"
)

var (
	debugGameGenscoresCmd = &cobra.Command{
		Use:   "genscores",
		Short: "genscores <phase>",
		Long:  debugGameGenscoresCmdLongDocs,
		Run:   debugGameGenscoresCmdRun,
		Args:  cobra.ExactArgs(1),
	}

	debugGameGenscoresCmdLongDocs = `debug game genscores <phase>

Generate scores for the given phase number.  DO NOT RUN THIS AGAINST A LIVE SERVER.  This command will remove all scorecards for the given phase and then refill the database with completely randomized scorecards.  This is useful for testing scoreboard information and output data that depends on having scores in the database.
`
)

func init() {
	debugGameCmd.AddCommand(debugGameGenscoresCmd)
}

func debugGameGenscoresCmdRun(c *cobra.Command, args []string) {
	d, err := db.New()
	if err != nil {
		slog.Error("Error initializing database", "error", err)
		os.Exit(2)
	}

	// This only partially initializes this module, which means
	// that we have to be careful what we call below since a lot
	// of things will explode with the module only partially
	// initialized.
	g := game.New(game.WithDatabase(d))

	gPhaseID, err := strconv.Atoi(args[0])
	if err != nil {
		slog.Error("You must specify a valid phase ID", "error", err)
		os.Exit(2)
	}

	if err := g.DebugGenerateScores(uint(gPhaseID)); err != nil {
		slog.Error("Error generating phase scores", "error", err)
		os.Exit(2)
	}
}
