package cmd

import (
	"github.com/spf13/cobra"
)

// listCommand returns a command that lists the repositories you can read.
func listCommand() *cobra.Command {
	var all bool

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List repositories",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repos, err := service(cmd.Context()).ListRepos(cmd.Context(), all)
			if err != nil {
				return err
			}
			for _, r := range repos {
				cmd.Println(r.Name)
			}
			return nil
		},
	}

	listCmd.Flags().BoolVarP(&all, "all", "a", false, "List all repositories")

	return listCmd
}
