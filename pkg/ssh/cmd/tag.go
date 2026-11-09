package cmd

import (
	"github.com/spf13/cobra"
)

func tagCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Manage repository tags",
	}

	cmd.AddCommand(
		tagListCommand(),
		tagDeleteCommand(),
	)

	return cmd
}

func tagListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list REPOSITORY",
		Aliases: []string{"ls"},
		Short:   "List repository tags",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := service(cmd.Context()).Repo(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, t := range r.Tags {
				cmd.Println(t)
			}
			return nil
		},
	}
}

func tagDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "delete REPOSITORY TAG",
		Aliases: []string{"remove", "rm", "del"},
		Short:   "Delete a tag",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return service(cmd.Context()).DeleteTag(cmd.Context(), args[0], args[1])
		},
	}
}
