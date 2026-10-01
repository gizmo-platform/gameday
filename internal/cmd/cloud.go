package cmd

import (
	"github.com/spf13/cobra"
)

var (
	cloudCmd = &cobra.Command{
		Use:   "cloud",
		Short: "cloud <command>",
		Long:  cloudCmdLongDocs,
	}

	cloudCmdLongDocs = `cloud <command>

Gameday runs certain services on-site and certain services in the cloud.  The cloud functionality is all contained within the 'cloud' command tree.  Use this set of commands when operating gameday from a remote/cloud host rather than physically on-site at an event.`
)

func init() {
	rootCmd.AddCommand(cloudCmd)
}
