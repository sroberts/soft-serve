package cmd

import (
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/spf13/cobra"
)

// createCommand is the command for creating a new repository.
func createCommand() *cobra.Command {
	var opts ops.CreateRepo

	cmd := &cobra.Command{
		Use:   "create REPOSITORY",
		Short: "Create a new repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			r, err := service(cmd.Context()).CreateRepo(cmd.Context(), opts)
			if err != nil {
				return err
			}

			cmd.PrintErrf("Created repository %s\n", r.Name)
			if r.SSHURL != "" {
				cmd.Println(r.SSHURL)
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&opts.Private, "private", "p", false, "make the repository private")
	cmd.Flags().StringVarP(&opts.Description, "description", "d", "", "set the repository description")
	cmd.Flags().StringVarP(&opts.ProjectName, "name", "n", "", "set the project name")
	cmd.Flags().BoolVarP(&opts.Hidden, "hidden", "H", false, "hide the repository from the UI")

	return cmd
}
