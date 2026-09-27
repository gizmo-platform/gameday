package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/gizmo-platform/gameday/modules/best"
	"github.com/gizmo-platform/gameday/modules/game"
)

var (
	debugGameLintCmd = &cobra.Command{
		Use:   "lint <file>",
		Short: "Lint a game setup file",
		Long:  debugGameLintCmdLongDocs,
		Args:  cobra.ExactArgs(1),
		Run:   debugGameLintCmdRun,
	}

	debugGameLintCmdLongDocs = `debug game lint <file>

Check a game setup file for problems before uploading it.  The file is
parsed the same way the setup upload parses it, and every expr
expression in it is compiled.  Every advancement filter, schedule
generator, tie-breaker, and source phase referenced by the file must
exist; otherwise the problem surfaces at runtime the first time the
offending phase is scheduled or advanced, with no context to go on.

The command exits with status 0 if the file is clean, 1 if problems
were found, and 2 if the file could not be read or parsed.
`
)

func init() {
	debugGameCmd.AddCommand(debugGameLintCmd)
}

func debugGameLintCmdRun(c *cobra.Command, args []string) {
	b, err := os.ReadFile(args[0])
	if err != nil {
		slog.Error("Error reading game setup file", "file", args[0], "error", err)
		os.Exit(2)
	}

	cfg := game.Config{}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		slog.Error("Error parsing game setup file", "file", args[0], "error", err)
		os.Exit(2)
	}

	// best.New registers the BEST module's advancement filter and
	// tie-breaker.  It is only partially initialized here, but that is
	// all the linter needs: a complete filter and tie-breaker registry.
	// It does not touch the database.
	best.New(nil, nil, nil)

	problems := game.LintConfig(cfg)
	if len(problems) == 0 {
		slog.Info("Game setup file is clean", "file", args[0])
		return
	}

	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	os.Exit(1)
}
