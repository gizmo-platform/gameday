package cmd

import (
	"github.com/spf13/cobra"
)

var (
	bakeCmd = &cobra.Command{
		Use:   "bake",
		Short: "bake <command>",
		Long:  bakeCmdLongDocs,
	}

	bakeCmdLongDocs = `bake <command>

Gameday makes use of certain pre-baked data that allows some normally computationally intensive actions to be "baked in" at build time so that this compute time is only spent once.  This allows most actions to be handled extremely quickly at runtime by using the cached values.

Most users will not need to use the bake commands.`
)

func init() {
	rootCmd.AddCommand(bakeCmd)
}
