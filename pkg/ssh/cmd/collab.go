package cmd

import (
	"github.com/spf13/cobra"
)

func collabCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "collab",
		Aliases: []string{"collabs", "collaborator", "collaborators"},
		Short:   "Manage collaborators",
	}

	cmd.AddCommand(
		collabAddCommand(),
		collabRemoveCommand(),
		collabListCommand(),
	)

	return cmd
}

func collabAddCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "add REPOSITORY USERNAME [LEVEL]",
		Short: "Add a collaborator to a repo",
		Long:  "Add a collaborator to a repo. LEVEL can be one of: no-access, read-only, read-write, or admin-access. Defaults to read-write.",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			var level string
			if len(args) > 2 {
				level = args[2]
			}
			return service(cmd.Context()).AddCollaborator(cmd.Context(), args[0], args[1], level)
		},
	}
}

func collabRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove REPOSITORY USERNAME",
		Args:  cobra.ExactArgs(2),
		Short: "Remove a collaborator from a repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			return service(cmd.Context()).RemoveCollaborator(cmd.Context(), args[0], args[1])
		},
	}
}

func collabListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list REPOSITORY",
		Short: "List collaborators for a repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			collabs, err := service(cmd.Context()).Collaborators(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, c := range collabs {
				cmd.Println(c.Username)
			}
			return nil
		},
	}
}
