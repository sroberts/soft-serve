package cmd

import (
	"github.com/spf13/cobra"
)

func renameCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "rename REPOSITORY NEW_NAME",
		Aliases: []string{"mv", "move"},
		Short:   "Rename an existing repository",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return service(cmd.Context()).RenameRepo(cmd.Context(), args[0], args[1])
		},
	}
}
