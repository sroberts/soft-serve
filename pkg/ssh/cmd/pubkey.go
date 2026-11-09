package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

// PubkeyCommand returns a command that manages user public keys.
func PubkeyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pubkey",
		Aliases: []string{"pubkeys", "publickey", "publickeys"},
		Short:   "Manage your public keys",
	}

	pubkeyAddCommand := &cobra.Command{
		Use:   "add AUTHORIZED_KEY",
		Short: "Add a public key",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, err := service(ctx).AddPublicKey(ctx, strings.Join(args, " "))
			return err
		},
	}

	pubkeyRemoveCommand := &cobra.Command{
		Use:   "remove AUTHORIZED_KEY|FINGERPRINT",
		Args:  cobra.MinimumNArgs(1),
		Short: "Remove a public key",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return service(ctx).RemovePublicKey(ctx, strings.Join(args, " "))
		},
	}

	pubkeyListCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List public keys",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			acct, err := service(ctx).Account(ctx)
			if err != nil {
				return err
			}
			for _, pk := range acct.PublicKeys {
				cmd.Println(pk.Key)
			}
			return nil
		},
	}

	cmd.AddCommand(
		pubkeyAddCommand,
		pubkeyRemoveCommand,
		pubkeyListCommand,
	)

	return cmd
}
