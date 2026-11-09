package cmd

import (
	"errors"
	"strconv"

	"charm.land/log/v2"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/spf13/cobra"
)

// syncCommand pulls a mirror from its upstream now.
func syncCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "sync REPOSITORY",
		Short:             "Sync a mirror from its upstream now",
		Args:              cobra.ExactArgs(1),
		PersistentPreRunE: requireRepo(access.ReadWriteAccess),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			rr, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			err = be.SyncMirror(ctx, rr)
			switch {
			case err == nil:
				return nil
			case errors.Is(err, proto.ErrNotMirror),
				errors.Is(err, proto.ErrImportsDisabled),
				errors.Is(err, proto.ErrMirrorSyncInProgress):
				return err
			default:
				// The detail says which hosts and ports answered; it
				// stays in the server log.
				log.FromContext(ctx).Error("mirror sync failed", "repo", rr.Name(), "err", err)
				return proto.ErrMirrorSyncFailed
			}
		},
	}
}

// autoSyncCommand gets or sets whether a mirror syncs on the server's
// schedule.
func autoSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "auto-sync REPOSITORY [true|false]",
		Short:             "Set or get whether a mirror syncs on the server's schedule",
		Args:              cobra.RangeArgs(1, 2),
		PersistentPreRunE: requireRepo(access.ReadOnlyAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			rr, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			if len(args) == 1 {
				if !rr.IsMirror() {
					return proto.ErrNotMirror
				}
				on, err := be.MirrorAutoSync(ctx, rr.Name())
				if err != nil {
					return err
				}
				cmd.Println(on)
				return nil
			}

			if err := requireLevel(ctx, access.ReadWriteAccess); err != nil {
				return err
			}
			on, err := strconv.ParseBool(args[1])
			if err != nil {
				return errors.New("want true or false")
			}
			return be.SetMirrorAutoSync(ctx, rr.Name(), on)
		},
	}
}
