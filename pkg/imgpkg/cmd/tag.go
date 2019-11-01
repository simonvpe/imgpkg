package cmd

import (
	"github.com/spf13/cobra"
)

func NewTagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Tag",
	}
	return cmd
}
