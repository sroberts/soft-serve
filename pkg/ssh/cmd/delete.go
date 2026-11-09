package cmd

import (
	"github.com/spf13/cobra"
)

func deleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "delete REPOSITORY",
		Aliases: []string{"del", "remove", "rm"},
		Short:   "Delete a repository",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return service(cmd.Context()).DeleteRepo(cmd.Context(), args[0])
		},
	}
}
