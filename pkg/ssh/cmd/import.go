package cmd

import (
	"errors"

	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/spf13/cobra"
)

// importCommand is the command for creating a new repository.
func importCommand() *cobra.Command {
	var private bool
	var description string
	var projectName string
	var mirror bool
	var autoSync bool
	var hidden bool
	var lfs bool
	var lfsEndpoint string

	cmd := &cobra.Command{
		Use:   "import REPOSITORY REMOTE",
		Short: "Import a new repository from remote",
		Long: `Import a new repository from remote.

By default the import is a hard fork: the remote is dropped once the
repository is cloned, and it is yours from then on. With --mirror it keeps
following the remote instead, synced on the server's mirror-pull schedule.
Add --auto-sync=false to sync a mirror only when you run "repo sync".`,
		Args:              cobra.ExactArgs(2),
		PersistentPreRunE: checkIfRepoCollab,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			user := proto.UserFromContext(ctx)
			name := args[0]
			remote := args[1]
			if cmd.Flags().Changed("auto-sync") && !mirror {
				return errors.New("--auto-sync only applies with --mirror")
			}
			if _, err := be.ImportRepository(ctx, name, user, remote, proto.RepositoryOptions{
				Private:     private,
				Description: description,
				ProjectName: projectName,
				Mirror:      mirror,
				ManualSync:  !autoSync,
				Hidden:      hidden,
				LFS:         lfs,
				LFSEndpoint: lfsEndpoint,
			}); err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&lfs, "lfs", "", false, "pull Git LFS objects")
	cmd.Flags().StringVarP(&lfsEndpoint, "lfs-endpoint", "", "", "set the Git LFS endpoint")
	cmd.Flags().BoolVarP(&mirror, "mirror", "m", false, "keep following the remote instead of forking it")
	cmd.Flags().BoolVarP(&autoSync, "auto-sync", "", true, "sync the mirror on the server's schedule (with --mirror)")
	cmd.Flags().BoolVarP(&private, "private", "p", false, "make the repository private")
	cmd.Flags().StringVarP(&description, "description", "d", "", "set the repository description")
	cmd.Flags().StringVarP(&projectName, "name", "n", "", "set the project name")
	cmd.Flags().BoolVarP(&hidden, "hidden", "H", false, "hide the repository from the UI")

	return cmd
}
