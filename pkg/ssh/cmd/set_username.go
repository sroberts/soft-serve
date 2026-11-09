package cmd

import (
	"github.com/spf13/cobra"
)

// SetUsernameCommand returns a command that sets the user's username.
func SetUsernameCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-username USERNAME",
		Short: "Set your username",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return service(ctx).SetUsername(ctx, args[0])
		},
	}

	return cmd
}
