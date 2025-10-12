package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	rootCmd = &cobra.Command{
		Use:   "gameday",
		Short: "gameday <command>",
		Long:  rootCmdLongDocs,
	}

	rootCmdLongDocs = `gameday is a suite of tools used to provide a complete competition management framework for multi-team robotics competitions.`
)

// Entrypoint enters the command subsystem and invokes the root cobra.Command
func Entrypoint() {
	logLevel := os.Getenv("LOG_LEVEL")
	var level slog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr,
		&slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		os.Exit(1)
	}
}
