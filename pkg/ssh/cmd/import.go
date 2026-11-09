package cmd

import (
	"errors"

	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/spf13/cobra"
)

// importCommand is the command for importing a repository from a remote.
func importCommand() *cobra.Command {
	var opts ops.CreateRepo
	var autoSync bool

	cmd := &cobra.Command{
		Use:   "import REPOSITORY REMOTE",
		Short: "Import a new repository from remote",
		Long: `Import a new repository from remote.

By default the import is a hard fork: the remote is dropped once the
repository is cloned, and it is yours from then on. With --mirror it keeps
following the remote instead, synced on the server's mirror-pull schedule.
Add --auto-sync=false to sync a mirror only when you run "repo sync".`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name, opts.Remote = args[0], args[1]
			if cmd.Flags().Changed("auto-sync") {
				if !opts.Mirror {
					return errors.New("--auto-sync only applies with --mirror")
				}
				opts.AutoSync = &autoSync
			}
			_, err := service(cmd.Context()).CreateRepo(cmd.Context(), opts)
			return err
		},
	}

	cmd.Flags().BoolVarP(&opts.LFS, "lfs", "", false, "pull Git LFS objects")
	cmd.Flags().StringVarP(&opts.LFSEndpoint, "lfs-endpoint", "", "", "set the Git LFS endpoint")
	cmd.Flags().BoolVarP(&opts.Mirror, "mirror", "m", false, "keep following the remote instead of forking it")
	cmd.Flags().BoolVarP(&autoSync, "auto-sync", "", true, "sync the mirror on the server's schedule (with --mirror)")
	cmd.Flags().BoolVarP(&opts.Private, "private", "p", false, "make the repository private")
	cmd.Flags().StringVarP(&opts.Description, "description", "d", "", "set the repository description")
	cmd.Flags().StringVarP(&opts.ProjectName, "name", "n", "", "set the project name")
	cmd.Flags().BoolVarP(&opts.Hidden, "hidden", "H", false, "hide the repository from the UI")

	return cmd
}
