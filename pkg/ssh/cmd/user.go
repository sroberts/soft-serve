package cmd

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/spf13/cobra"
)

// parseBool reads a true or false argument.
func parseBool(s string) (bool, error) {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, ops.Invalid("want true or false, not %q", s)
	}
	return b, nil
}

// UserCommand returns the user subcommand.
func UserCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "user",
		Aliases: []string{"users"},
		Short:   "Manage users",
		// Gate the whole command tree rather than each subcommand. Cobra
		// runs the nearest persistent pre-run found when walking up from
		// the invoked command, so subcommands added later are gated by
		// default instead of silently unprotected. The service checks
		// again, which is what protects the soft client's copy.
		PersistentPreRunE: checkIfServerAdmin,
	}

	var admin bool
	var key string
	userCreateCommand := &cobra.Command{
		Use:   "create USERNAME",
		Short: "Create a new user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			opts := ops.CreateUser{Username: args[0], Admin: admin}
			if key != "" {
				opts.Keys = []string{key}
			}
			_, err := service(ctx).CreateUser(ctx, opts)
			return err
		},
	}

	userCreateCommand.Flags().BoolVarP(&admin, "admin", "a", false, "make the user an admin")
	userCreateCommand.Flags().StringVarP(&key, "key", "k", "", "add a public key to the user")

	userDeleteCommand := &cobra.Command{
		Use:   "delete USERNAME",
		Short: "Delete a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return service(ctx).DeleteUser(ctx, args[0])
		},
	}

	userListCommand := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List users",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			users, err := service(ctx).Users(ctx)
			if err != nil {
				return err
			}
			for _, u := range users {
				cmd.Println(u.Username)
			}
			return nil
		},
	}

	userAddPubkeyCommand := &cobra.Command{
		Use:   "add-pubkey USERNAME AUTHORIZED_KEY",
		Short: "Add a public key to a user",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			_, err := service(ctx).AddUserKey(ctx, args[0], strings.Join(args[1:], " "))
			return err
		},
	}

	userRemovePubkeyCommand := &cobra.Command{
		Use:   "remove-pubkey USERNAME AUTHORIZED_KEY|FINGERPRINT",
		Short: "Remove a public key from a user",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return service(ctx).RemoveUserKey(ctx, args[0], strings.Join(args[1:], " "))
		},
	}

	userSetAdminCommand := &cobra.Command{
		Use:   "set-admin USERNAME [true|false]",
		Short: "Make a user an admin",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := parseBool(args[1])
			if err != nil {
				return err
			}
			return service(ctx).UpdateUser(ctx, args[0], ops.UserUpdate{Admin: &b})
		},
	}

	userInfoCommand := &cobra.Command{
		Use:   "info USERNAME",
		Short: "Show information about a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			u, err := service(ctx).User(ctx, args[0])
			if err != nil {
				return err
			}

			cmd.Printf("Username: %s\n", u.Username)
			cmd.Printf("Admin: %t\n", u.Admin)
			cmd.Printf("Public keys:\n")
			for _, pk := range u.PublicKeys {
				cmd.Printf("  %s\n", pk.Key)
			}

			return nil
		},
	}

	userSetUsernameCommand := &cobra.Command{
		Use:   "set-username USERNAME NEW_USERNAME",
		Short: "Change a user's username",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return service(ctx).UpdateUser(ctx, args[0], ops.UserUpdate{Username: &args[1]})
		},
	}

	cmd.AddCommand(
		userCreateCommand,
		userAddPubkeyCommand,
		userInfoCommand,
		userListCommand,
		userDeleteCommand,
		userRemovePubkeyCommand,
		userSetAdminCommand,
		userSetUsernameCommand,
	)

	return cmd
}
