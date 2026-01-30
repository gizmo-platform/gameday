package cmd

import (
	"github.com/spf13/cobra"
)

var (
	debugGameCmd = &cobra.Command{
		Use:   "game",
		Short: "<command>",
		Long:  debugGameCmdLongDocs,
	}

	debugGameCmdLongDocs = `debug game <command>

Manipulate items related to the game module.  USE WITH CAUTION.
`
)

func init() {
	debugCmd.AddCommand(debugGameCmd)
}
