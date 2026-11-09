package cmd

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/spf13/cobra"
)

// repoSetting is a "get or set" repository command: with only the
// repository it prints the setting, and with a value it changes it.
type repoSetting struct {
	use, short string
	aliases    []string
	// args bounds the arguments, the repository included.
	args cobra.PositionalArgs
	// get prints the setting from the repository.
	get func(cmd *cobra.Command, r ops.Repo) error
	// set turns the value arguments into the change to make.
	set func(values []string) (ops.RepoUpdate, error)
}

func (s repoSetting) command() *cobra.Command {
	return &cobra.Command{
		Use:     s.use,
		Aliases: s.aliases,
		Short:   s.short,
		Args:    s.args,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc := service(ctx)
			if len(args) == 1 {
				r, err := svc.Repo(ctx, args[0])
				if err != nil {
					return err
				}
				return s.get(cmd, r)
			}
			u, err := s.set(args[1:])
			if err != nil {
				return err
			}
			return svc.UpdateRepo(ctx, args[0], u)
		},
	}
}

func descriptionCommand() *cobra.Command {
	return repoSetting{
		use:     "description REPOSITORY [DESCRIPTION]",
		aliases: []string{"desc"},
		short:   "Set or get the description for a repository",
		args:    cobra.MinimumNArgs(1),
		get: func(cmd *cobra.Command, r ops.Repo) error {
			cmd.Println(r.Description)
			return nil
		},
		set: func(v []string) (ops.RepoUpdate, error) {
			d := strings.Join(v, " ")
			return ops.RepoUpdate{Description: &d}, nil
		},
	}.command()
}

func projectName() *cobra.Command {
	return repoSetting{
		use:     "project-name REPOSITORY [NAME]",
		aliases: []string{"project"},
		short:   "Set or get the project name for a repository",
		args:    cobra.MinimumNArgs(1),
		get: func(cmd *cobra.Command, r ops.Repo) error {
			cmd.Println(r.ProjectName)
			return nil
		},
		set: func(v []string) (ops.RepoUpdate, error) {
			n := strings.Join(v, " ")
			return ops.RepoUpdate{ProjectName: &n}, nil
		},
	}.command()
}

func privateCommand() *cobra.Command {
	return repoSetting{
		use:   "private REPOSITORY [true|false]",
		short: "Set or get a repository private property",
		args:  cobra.RangeArgs(1, 2),
		get: func(cmd *cobra.Command, r ops.Repo) error {
			cmd.Println(r.Private)
			return nil
		},
		set: func(v []string) (ops.RepoUpdate, error) {
			b, err := strconv.ParseBool(v[0])
			return ops.RepoUpdate{Private: &b}, err
		},
	}.command()
}

func hiddenCommand() *cobra.Command {
	return repoSetting{
		use:     "hidden REPOSITORY [TRUE|FALSE]",
		aliases: []string{"hide"},
		short:   "Hide or unhide a repository",
		args:    cobra.RangeArgs(1, 2),
		get: func(cmd *cobra.Command, r ops.Repo) error {
			cmd.Println(r.Hidden)
			return nil
		},
		set: func(v []string) (ops.RepoUpdate, error) {
			b := v[0] == "true"
			return ops.RepoUpdate{Hidden: &b}, nil
		},
	}.command()
}

func mirrorCommand() *cobra.Command {
	return repoSetting{
		use:   "mirror REPOSITORY [true|false]",
		short: "Set or get a repository mirror property",
		args:  cobra.RangeArgs(1, 2),
		get: func(cmd *cobra.Command, r ops.Repo) error {
			cmd.Println(r.Mirror)
			return nil
		},
		set: func(v []string) (ops.RepoUpdate, error) {
			b := v[0] == "true"
			return ops.RepoUpdate{Mirror: &b}, nil
		},
	}.command()
}

// autoSyncCommand gets or sets whether a mirror syncs on the server's
// schedule.
func autoSyncCommand() *cobra.Command {
	return repoSetting{
		use:   "auto-sync REPOSITORY [true|false]",
		short: "Set or get whether a mirror syncs on the server's schedule",
		args:  cobra.RangeArgs(1, 2),
		get: func(cmd *cobra.Command, r ops.Repo) error {
			if r.AutoSync == nil {
				return proto.ErrNotMirror
			}
			cmd.Println(*r.AutoSync)
			return nil
		},
		set: func(v []string) (ops.RepoUpdate, error) {
			b, err := strconv.ParseBool(v[0])
			if err != nil {
				return ops.RepoUpdate{}, ops.Invalid("want true or false")
			}
			return ops.RepoUpdate{AutoSync: &b}, nil
		},
	}.command()
}

// syncCommand pulls a mirror from its upstream now.
func syncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync REPOSITORY",
		Short: "Sync a mirror from its upstream now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return service(cmd.Context()).SyncMirror(cmd.Context(), args[0])
		},
	}
}
