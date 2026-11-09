package cmd

import (
	"fmt"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/spf13/cobra"
)

// SettingsCommand returns a command that manages server settings.
func SettingsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Manage server settings",
		// Gate the whole command tree rather than each subcommand. Cobra
		// runs the nearest persistent pre-run found when walking up from
		// the invoked command, so subcommands added later are gated by
		// default instead of silently unprotected. The service checks
		// again, which is what protects the soft client's copy.
		PersistentPreRunE: checkIfServerAdmin,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "allow-keyless [true|false]",
			Short: "Set or get allow keyless access to repositories",
			Args:  cobra.RangeArgs(0, 1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				svc := service(ctx)
				if len(args) == 0 {
					s, err := svc.Settings(ctx)
					if err != nil {
						return err
					}
					cmd.Println(s.AllowKeyless)
					return nil
				}
				v, err := parseBool(args[0])
				if err != nil {
					return err
				}
				s, err := svc.UpdateSettings(ctx, ops.SettingsUpdate{AllowKeyless: &v})
				if err != nil {
					return err
				}
				if s.AllowKeylessFixed {
					// Without this, an admin changing the setting would
					// have no way to know the change has no effect.
					fmt.Fprintf(cmd.ErrOrStderr(),
						"Warning: allow-keyless is set to %t by server config and takes precedence over this change. "+
							"The database was updated, but it will have no effect until the config override is removed.\n",
						s.AllowKeyless)
				}
				return nil
			},
		},
	)

	als := []string{access.NoAccess.String(), access.ReadOnlyAccess.String(), access.ReadWriteAccess.String(), access.AdminAccess.String()}
	cmd.AddCommand(
		&cobra.Command{
			Use:       "anon-access [ACCESS_LEVEL]",
			Short:     "Set or get the default access level for anonymous users",
			Args:      cobra.RangeArgs(0, 1),
			ValidArgs: als,
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				svc := service(ctx)
				if len(args) == 0 {
					s, err := svc.Settings(ctx)
					if err != nil {
						return err
					}
					cmd.Println(s.AnonAccess)
					return nil
				}
				s, err := svc.UpdateSettings(ctx, ops.SettingsUpdate{AnonAccess: &args[0]})
				if err != nil {
					return err
				}
				if s.AnonAccessFixed {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"Warning: anon-access is set to %q by server config and takes precedence over this change. "+
							"The database was updated, but it will have no effect until the config override is removed.\n",
						s.AnonAccess)
				}
				return nil
			},
		},
	)

	return cmd
}

// AdminCommands are the server admin commands. They act through the
// context's ops.Service, so the soft client runs them too.
func AdminCommands() []*cobra.Command {
	return []*cobra.Command{UserCommand(), SettingsCommand()}
}
