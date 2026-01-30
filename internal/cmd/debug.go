package cmd

import (
	"github.com/spf13/cobra"
)

var (
	debugCmd = &cobra.Command{
		Use:   "debug",
		Short: "debug <command>",
		Long:  debugCmdLongDocs,
	}

	debugCmdLongDocs = `debug <command>

Debug items are special tools that are used to directly manipulate internal state in ways that are not normally possible via the frontend interfaces.  Use these commands with extreme care as they can and will absolutely trash your database.
`
)

func init() {
	rootCmd.AddCommand(debugCmd)
}
