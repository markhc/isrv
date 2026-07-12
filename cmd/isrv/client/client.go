package client

import (
	"github.com/spf13/cobra"
)

var putCmd = &cobra.Command{
	Use:   "put",
	Short: "Upload a file to the isrv server",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Download a file from the isrv server",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var rmCmd = &cobra.Command{
	Use:   "rm",
	Short: "Remove a file from the isrv server",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List your files on the isrv server",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var ClientCmds = []*cobra.Command{
	putCmd,
	getCmd,
	rmCmd,
	lsCmd,
}
