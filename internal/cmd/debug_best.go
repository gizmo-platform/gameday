package cmd

import (
	"github.com/spf13/cobra"
)

var (
	debugBestCmd = &cobra.Command{
		Use:   "best",
		Short: "<command>",
		Long:  debugBestCmdLongDocs,
	}

	debugBestCmdLongDocs = `debug best <command>

Manipulate items related to the best module.  USE WITH CAUTION.
`
)

func init() {
	debugCmd.AddCommand(debugBestCmd)
}
