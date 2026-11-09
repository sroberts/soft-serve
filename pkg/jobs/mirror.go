package jobs

import (
	"context"
	"errors"
	"runtime"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/sync"
)

func init() {
	Register("mirror-pull", mirrorPull{})
}

type mirrorPull struct{}

// Spec derives the spec used for pull mirrors and implements Runner.
func (m mirrorPull) Spec(ctx context.Context) string {
	cfg := config.FromContext(ctx)
	if cfg.Jobs.MirrorPull != "" {
		return cfg.Jobs.MirrorPull
	}
	return "@every 10m"
}

// Func runs the (pull) mirror job task and implements Runner. It syncs every
// mirror that has not been set to sync only on demand.
func (m mirrorPull) Func(ctx context.Context) func() {
	logger := log.FromContext(ctx).WithPrefix("jobs.mirror")
	b := backend.FromContext(ctx)
	return func() {
		// The same guards that protect imports protect syncs, and the same
		// too-old git ignores them.
		if reason := b.ImportsDisabledReason(); reason != "" {
			logger.Warn("skipping mirror sync", "reason", reason)
			return
		}

		repos, err := b.Repositories(ctx)
		if err != nil {
			logger.Error("error getting repositories", "err", err)
			return
		}

		// Divide the work up among the number of CPUs.
		wq := sync.NewWorkPool(ctx, runtime.GOMAXPROCS(0),
			sync.WithWorkPoolLogger(logger.Errorf),
		)

		logger.Debug("updating mirror repos")
		for _, repo := range repos {
			if !repo.IsMirror() {
				continue
			}

			name := repo.Name()
			auto, err := b.MirrorAutoSync(ctx, name)
			if err != nil {
				logger.Error("error opening repository", "repo", name, "err", err)
				continue
			}
			if !auto {
				logger.Debug("skipping mirror, autosync is off", "repo", name)
				continue
			}

			wq.Add(name, func() {
				switch err := b.SyncMirror(ctx, repo); {
				case errors.Is(err, proto.ErrMirrorSyncInProgress):
					logger.Debug("skipping mirror, already syncing", "repo", name)
				case err != nil:
					logger.Error("error syncing mirror", "repo", name, "err", err)
				}
			})
		}

		wq.Run()
	}
}
