package cmd

import (
	"github.com/spf13/cobra"
)

// InfoCommand returns a command that shows the user's info
func InfoCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show your info",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			acct, err := service(ctx).Account(ctx)
			if err != nil {
				return err
			}

			cmd.Printf("Username: %s\n", acct.Username)
			cmd.Printf("Admin: %t\n", acct.Admin)
			cmd.Printf("Public keys:\n")
			for _, pk := range acct.PublicKeys {
				cmd.Printf("  %s\n", pk.Key)
			}
			return nil
		},
	}

	return cmd
}

// AccountCommands are the commands about the caller's own account. They act
// through the context's ops.Service, so the soft client runs them too.
func AccountCommands() []*cobra.Command {
	return []*cobra.Command{
		InfoCommand(),
		PubkeyCommand(),
		SetUsernameCommand(),
		TokenCommand(),
	}
}
