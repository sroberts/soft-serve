package backend

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/soft-serve/git"
	"github.com/charmbracelet/soft-serve/pkg/lfs"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/ssrf"
	"github.com/charmbracelet/soft-serve/pkg/utils"
)

// A mirror's own settings live in its git config, next to the remote they
// are about, under [softserve]. autosync = false takes the mirror out of the
// scheduled mirror-pull job; it still syncs on demand. A missing key means
// true, which is what every mirror did before the setting existed.
const (
	mirrorConfigSection = "softserve"
	mirrorAutoSyncKey   = "autosync"
)

// MirrorAutoSyncOf reports whether the scheduled job should sync r. A config
// that cannot be read keeps the default rather than silently stopping syncs.
func MirrorAutoSyncOf(r *git.Repository) bool {
	cfg, err := r.Config()
	if err != nil {
		return true
	}
	v := cfg.Section(mirrorConfigSection).Option(mirrorAutoSyncKey)
	if v == "" {
		return true
	}
	on, err := strconv.ParseBool(v)
	return err != nil || on
}

// MirrorAutoSync reports whether the scheduled mirror-pull job syncs the
// named repository. It says nothing about whether the repository is a
// mirror; the job only looks at mirrors.
func (d *Backend) MirrorAutoSync(ctx context.Context, name string) (bool, error) {
	rr, err := d.Repository(ctx, name)
	if err != nil {
		return false, err
	}
	r, err := rr.Open()
	if err != nil {
		return false, err
	}
	return MirrorAutoSyncOf(r), nil
}

// SetMirrorAutoSync turns the scheduled sync of a mirror on or off. Only a
// mirror has anything to sync, so other repositories are refused.
func (d *Backend) SetMirrorAutoSync(ctx context.Context, name string, on bool) error {
	name = utils.SanitizeRepo(name)
	rr, err := d.Repository(ctx, name)
	if err != nil {
		return err
	}
	if !rr.IsMirror() {
		return proto.ErrNotMirror
	}
	r, err := rr.Open()
	if err != nil {
		d.logger.Error("failed to open repository", "err", err, "repo", name)
		return errRepoUpdate
	}
	if err := setMirrorAutoSync(r, on); err != nil {
		d.logger.Error("failed to set mirror autosync", "err", err, "repo", name)
		return errRepoUpdate
	}
	return nil
}

func setMirrorAutoSync(r *git.Repository, on bool) error {
	cfg, err := r.Config()
	if err != nil {
		return err
	}
	cfg.Section(mirrorConfigSection).SetOption(mirrorAutoSyncKey, strconv.FormatBool(on))
	return r.SetConfig(cfg)
}

// detachRemotes removes every remote from a freshly cloned repository so it
// is a hard fork: nothing in it points back at where it came from, and it
// can never be turned into a mirror of it.
func detachRemotes(r *git.Repository) error {
	cfg, err := r.Config()
	if err != nil {
		return err
	}
	cfg.RemoveSection("remote")
	return r.SetConfig(cfg)
}

// validateMirrorRemotes validates every remote configured on a mirror
// repository against address ranges and the ssh host policy, and returns
// them so the caller can render the git environment that must be applied to
// the sync commands.
//
// `git remote update` fetches from all remotes, not just origin, so every one
// of them has to pass. Any failure to read or validate skips the sync: a
// remote that cannot be checked is not one to fetch from.
func validateMirrorRemotes(r *git.Repository, policy ssrf.GitRemotePolicy) ([]ssrf.ValidatedGitRemote, error) {
	cfg, err := r.Config()
	if err != nil {
		return nil, fmt.Errorf("reading git config: %w", err)
	}

	var remotes []ssrf.ValidatedGitRemote
	for _, sub := range cfg.Section("remote").Subsections {
		url := sub.Option("url")
		if url == "" {
			continue
		}

		v, err := ssrf.ValidateGitRemote(url, policy)
		if err != nil {
			return nil, fmt.Errorf("remote %q: %w", sub.Name, err)
		}
		remotes = append(remotes, v)
	}

	if len(remotes) == 0 {
		return nil, fmt.Errorf("no remote url configured")
	}

	return remotes, nil
}

// SyncMirror fetches a mirror from its upstream, pruning what the upstream
// deleted, and then any LFS objects it is missing. The scheduled job and an
// on-demand `repo sync` both come through here.
//
// The returned error is detailed and meant for the log. A user should be
// shown proto.ErrMirrorSyncFailed instead, so a sync cannot be used to probe
// hosts and ports, except for the sentinel errors from package proto, which
// are safe to show as they are.
func (d *Backend) SyncMirror(ctx context.Context, repo proto.Repository) error {
	if reason := d.ImportsDisabledReason(); reason != "" {
		d.logger.Warn("refusing mirror sync", "repo", repo.Name(), "reason", reason)
		return proto.ErrImportsDisabled
	}
	if !repo.IsMirror() {
		return proto.ErrNotMirror
	}

	name := repo.Name()
	// The job and a user can ask for the same mirror at once, and two
	// fetches into one repository trip over each other's ref locks.
	if _, busy := d.syncing.LoadOrStore(name, struct{}{}); busy {
		return proto.ErrMirrorSyncInProgress
	}
	defer d.syncing.Delete(name)

	r, err := repo.Open()
	if err != nil {
		return fmt.Errorf("opening repository: %w", err)
	}

	// Re-validate every configured remote before syncing. `remote update`
	// touches all of them, and a remote may predate the import-time guard or
	// have been written out of band. The owner set the remote up, so the
	// owner's policy applies whoever asks for the sync. An owner that cannot
	// be looked up gets the policy of nobody, which refuses ssh without an
	// allowlist.
	owner, err := d.UserByID(ctx, repo.UserID())
	if err != nil {
		d.logger.Warn("mirror owner not found, treating as unprivileged", "repo", name, "err", err)
		owner = nil
	}
	policy := RemotePolicy(d.cfg, owner)

	remotes, err := validateMirrorRemotes(r, policy)
	if err != nil {
		return fmt.Errorf("remote failed validation: %w", err)
	}

	syncEnv := RemoteGitEnv(d.cfg, remotes...)

	var errs []error
	for _, c := range []string{
		"fetch --prune",         // fetch prune before updating remote
		"remote update --prune", // update remote and prune remote refs
	} {
		args := strings.Split(c, " ")
		cmd := git.NewCommand(args...).WithContext(ctx).WithTimeout(-1)
		cmd.AddEnvs(syncEnv...)

		if _, err := cmd.RunInDir(r.Path); err != nil {
			errs = append(errs, fmt.Errorf("git %s: %w", c, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}

	if !d.cfg.LFS.Enabled {
		return nil
	}

	rcfg, err := r.Config()
	if err != nil {
		return fmt.Errorf("reading git config: %w", err)
	}

	lfsEndpoint := rcfg.Section("lfs").Option("url")
	if lfsEndpoint == "" {
		// No LFS url means the repo doesn't use LFS.
		return nil
	}

	// The endpoint is stored in the repo config and may predate validation,
	// so check it before dialing.
	if _, err := ssrf.ValidateGitRemote(lfsEndpoint, policy); err != nil {
		return fmt.Errorf("lfs endpoint failed validation: %w", err)
	}

	ep, err := lfs.NewEndpoint(lfsEndpoint)
	if err != nil {
		return fmt.Errorf("creating lfs endpoint: %w", err)
	}

	client := lfs.NewClient(ep)
	if client == nil {
		return fmt.Errorf("unsupported lfs endpoint %s", redactRemote(lfsEndpoint))
	}

	if err := StoreRepoMissingLFSObjects(ctx, repo, d.db, d.store, client); err != nil {
		return fmt.Errorf("storing missing lfs objects: %w", err)
	}

	return nil
}
