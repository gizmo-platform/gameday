package cmd

import (
	"github.com/spf13/cobra"
)

var (
	onsiteCmd = &cobra.Command{
		Use:   "onsite",
		Short: "onsite <command>",
		Long:  onsiteCmdLongDocs,
	}

	onsiteCmdLongDocs = `onsite <command>

Gameday runs certain services on-site and certain services in the cloud.  The on-site functionality is all contained within the 'onsite' command tree.  Use this set of commands whenever physically on-site at an event.`
)

func init() {
	rootCmd.AddCommand(onsiteCmd)
}
