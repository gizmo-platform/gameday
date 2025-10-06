package cmd

import (
	"fmt"
	"os"

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
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		os.Exit(1)
	}
}
