package ops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"charm.land/log/v2"
	gitm "github.com/aymanbagabas/git-module"
	"github.com/charmbracelet/soft-serve/git"
	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/utils"
	"github.com/charmbracelet/soft-serve/pkg/webhook"
	"github.com/google/uuid"
)

// Local is a Service on the server, for one actor, over the backend.
type Local struct {
	be    *backend.Backend
	actor Actor
}

var _ Service = (*Local)(nil)

// NewLocal returns the Service for actor.
func NewLocal(be *backend.Backend, actor Actor) *Local {
	return &Local{be: be, actor: actor}
}

// Actor returns who this Service acts for.
func (l *Local) Actor() Actor { return l.actor }

// withUser makes sure the backend sees the actor, which it reads from the
// context for webhook events and the like.
func (l *Local) withUser(ctx context.Context) context.Context {
	if l.actor.User != nil && proto.UserFromContext(ctx) == nil {
		return proto.WithUserContext(ctx, l.actor.User)
	}
	return ctx
}

// denyRead is the one answer for a repository the actor may not read,
// whether it is missing or private: unauthorized for an anonymous caller,
// not found for everyone else. Admins always get not found, since nothing
// is hidden from them.
func (l *Local) denyRead() error {
	if l.actor.User == nil && !l.actor.Admin {
		return proto.ErrUnauthorized
	}
	return proto.ErrRepoNotFound
}

// readable returns the repository if the actor can read it, and the
// actor's access level for the name either way.
func (l *Local) readable(ctx context.Context, name string) (proto.Repository, access.AccessLevel) {
	if l.actor.Admin {
		r, _ := l.be.Repository(ctx, name)
		return r, access.AdminAccess
	}
	return l.be.Readable(ctx, name, l.actor.User)
}

// repo is the gate for an existing repository: the actor must be able to
// read it, and hold at least need. Readability is decided first, so a
// caller who cannot read gets the same answer whether or not it exists.
func (l *Local) repo(ctx context.Context, name string, need access.AccessLevel) (proto.Repository, access.AccessLevel, error) {
	r, level := l.readable(ctx, utils.SanitizeRepo(name))
	if r == nil {
		return nil, level, l.denyRead()
	}
	if level < need {
		return nil, level, proto.ErrUnauthorized
	}
	return r, level, nil
}

// creatable is the gate for a name that should not exist yet: the actor
// needs write access to it. With one flat namespace this cannot hide
// whether the name is taken; "already exists" is the answer either way.
func (l *Local) creatable(ctx context.Context, name string) (access.AccessLevel, error) {
	_, level := l.readable(ctx, utils.SanitizeRepo(name))
	if level < access.ReadWriteAccess {
		return level, proto.ErrUnauthorized
	}
	return level, nil
}

// ListRepos implements Service.
func (l *Local) ListRepos(ctx context.Context, all bool) ([]RepoSummary, error) {
	ctx = l.withUser(ctx)
	repos, err := l.be.Repositories(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RepoSummary, 0, len(repos))
	for _, r := range repos {
		if r.IsHidden() && !all {
			continue
		}
		if !l.actor.Admin && l.be.AccessLevelForUser(ctx, r.Name(), l.actor.User) < access.ReadOnlyAccess {
			continue
		}
		out = append(out, summary(r))
	}
	return out, nil
}

func summary(r proto.Repository) RepoSummary {
	return RepoSummary{
		Name:        r.Name(),
		ProjectName: r.ProjectName(),
		Description: r.Description(),
		Private:     r.IsPrivate(),
		Hidden:      r.IsHidden(),
		Mirror:      r.IsMirror(),
	}
}

// Repo implements Service.
func (l *Local) Repo(ctx context.Context, name string) (Repo, error) {
	ctx = l.withUser(ctx)
	r, level, err := l.repo(ctx, name, access.ReadOnlyAccess)
	if err != nil {
		return Repo{}, err
	}
	return l.describe(ctx, r, level)
}

func (l *Local) describe(ctx context.Context, r proto.Repository, level access.AccessLevel) (Repo, error) {
	out := Repo{RepoSummary: summary(r), Access: level.String(), Branches: []string{}, Tags: []string{}}
	if cfg := config.FromContext(ctx); cfg != nil && cfg.SSH.PublicURL != "" {
		out.SSHURL = fmt.Sprintf("%s/%s.git", cfg.SSH.PublicURL, r.Name())
	}
	if r.UserID() > 0 {
		if owner, err := l.be.UserByID(ctx, r.UserID()); err == nil {
			out.Owner = owner.Username()
		}
	}

	gr, err := r.Open()
	if err != nil {
		return Repo{}, err
	}
	if r.IsMirror() {
		on := backend.MirrorAutoSyncOf(gr)
		out.AutoSync = &on
	}
	if head, err := gr.HEAD(); err == nil {
		out.DefaultBranch = head.Name().Short()
	}
	if bs, err := gr.Branches(); err == nil && bs != nil {
		out.Branches = bs
	}
	if ts, err := gr.Tags(); err == nil && ts != nil {
		out.Tags = ts
	}
	return out, nil
}

// CreateRepo implements Service.
func (l *Local) CreateRepo(ctx context.Context, opts CreateRepo) (Repo, error) {
	ctx = l.withUser(ctx)
	level, err := l.creatable(ctx, opts.Name)
	if err != nil {
		return Repo{}, err
	}

	ropts := proto.RepositoryOptions{
		Private:     opts.Private,
		Description: opts.Description,
		ProjectName: opts.ProjectName,
		Hidden:      opts.Hidden,
	}

	var r proto.Repository
	if opts.Remote == "" {
		if opts.Mirror || opts.AutoSync != nil || opts.LFS || opts.LFSEndpoint != "" {
			return Repo{}, Invalid("mirror, auto_sync and lfs only apply to an import")
		}
		r, err = l.be.CreateRepository(ctx, opts.Name, l.actor.User, ropts)
	} else {
		if opts.AutoSync != nil && !opts.Mirror {
			return Repo{}, Invalid("auto_sync only applies to a mirror")
		}
		ropts.Mirror = opts.Mirror
		ropts.ManualSync = opts.AutoSync != nil && !*opts.AutoSync
		ropts.LFS = opts.LFS
		ropts.LFSEndpoint = opts.LFSEndpoint
		r, err = l.be.ImportRepository(ctx, opts.Name, l.actor.User, opts.Remote, ropts)
	}
	if err != nil {
		return Repo{}, err
	}

	// The actor just made it, so describe it at the level that let them,
	// rather than gating the read; an anonymous creator may not be able to
	// read a private repository back.
	if level < access.ReadOnlyAccess {
		level = access.ReadOnlyAccess
	}
	return l.describe(ctx, r, level)
}

// DeleteRepo implements Service.
func (l *Local) DeleteRepo(ctx context.Context, name string) error {
	ctx = l.withUser(ctx)
	name = utils.SanitizeRepo(name)
	// An admin may delete by name alone, which clears out a database row
	// whose directory is gone.
	if !l.actor.Admin {
		if _, _, err := l.repo(ctx, name, access.ReadWriteAccess); err != nil {
			return err
		}
	}
	return l.be.DeleteRepository(ctx, name)
}

// RenameRepo implements Service.
func (l *Local) RenameRepo(ctx context.Context, name, newName string) error {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	newName = utils.SanitizeRepo(newName)
	if _, err := l.creatable(ctx, newName); err != nil {
		return err
	}
	return l.be.RenameRepository(ctx, r.Name(), newName)
}

// UpdateRepo implements Service.
func (l *Local) UpdateRepo(ctx context.Context, name string, u RepoUpdate) error {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	name = r.Name()

	if u.ProjectName != nil {
		if err := l.be.SetProjectName(ctx, name, *u.ProjectName); err != nil {
			return err
		}
	}
	if u.Description != nil {
		if err := l.be.SetDescription(ctx, name, *u.Description); err != nil {
			return err
		}
	}
	if u.Private != nil {
		if err := l.be.SetPrivate(ctx, name, *u.Private); err != nil {
			return err
		}
	}
	if u.Hidden != nil {
		if err := l.be.SetHidden(ctx, name, *u.Hidden); err != nil {
			return err
		}
	}
	if u.Mirror != nil {
		if err := l.be.SetMirror(ctx, name, *u.Mirror); err != nil {
			return err
		}
	}
	if u.AutoSync != nil {
		if err := l.be.SetMirrorAutoSync(ctx, name, *u.AutoSync); err != nil {
			return err
		}
	}
	if u.DefaultBranch != nil {
		// Re-read: an earlier change in this update may have touched it.
		r, err := l.be.Repository(ctx, name)
		if err != nil {
			return err
		}
		if err := l.setDefaultBranch(ctx, r, *u.DefaultBranch); err != nil {
			return err
		}
	}
	return nil
}

func (l *Local) setDefaultBranch(ctx context.Context, r proto.Repository, branch string) error {
	gr, err := r.Open()
	if err != nil {
		return err
	}
	branches, _ := gr.Branches()
	if !slices.Contains(branches, branch) {
		return git.ErrReferenceNotExist
	}
	if _, err := gr.SymbolicRef(git.HEAD, gitm.RefsHeads+branch, gitm.SymbolicRefOptions{
		CommandOptions: gitm.CommandOptions{Context: ctx},
	}); err != nil {
		return err
	}
	wh, err := webhook.NewRepositoryEvent(ctx, l.actor.User, r, webhook.RepositoryEventActionDefaultBranchChange)
	if err != nil {
		return err
	}
	return webhook.SendEvent(ctx, wh)
}

// SyncMirror implements Service. Failures other than the ones the caller
// can act on are logged and reported as proto.ErrMirrorSyncFailed, so a
// sync cannot be used to probe hosts and ports.
func (l *Local) SyncMirror(ctx context.Context, name string) error {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	err = l.be.SyncMirror(ctx, r)
	switch {
	case err == nil,
		errors.Is(err, proto.ErrNotMirror),
		errors.Is(err, proto.ErrImportsDisabled),
		errors.Is(err, proto.ErrMirrorSyncInProgress):
		return err
	default:
		log.FromContext(ctx).Error("mirror sync failed", "repo", r.Name(), "err", err)
		return proto.ErrMirrorSyncFailed
	}
}

// DeleteBranch implements Service.
func (l *Local) DeleteBranch(ctx context.Context, name, branch string) error {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	gr, err := r.Open()
	if err != nil {
		return err
	}
	branches, _ := gr.Branches()
	if !slices.Contains(branches, branch) {
		return git.ErrReferenceNotExist
	}
	head, err := gr.HEAD()
	if err != nil {
		return err
	}
	if head.Name().Short() == branch {
		return Invalid("cannot delete the default branch")
	}
	commit, err := gr.BranchCommit(branch)
	if err != nil {
		return err
	}
	if err := gr.DeleteBranch(branch, gitm.DeleteBranchOptions{Force: true}); err != nil {
		return err
	}
	wh, err := webhook.NewBranchTagEvent(ctx, l.actor.User, r, git.RefsHeads+branch, commit.ID.String(), git.ZeroID)
	if err != nil {
		return err
	}
	return webhook.SendEvent(ctx, wh)
}

// DeleteTag implements Service.
func (l *Local) DeleteTag(ctx context.Context, name, tag string) error {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	gr, err := r.Open()
	if err != nil {
		return err
	}
	tags, _ := gr.Tags()
	if !slices.Contains(tags, tag) {
		return git.ErrReferenceNotExist
	}
	commit, err := gr.TagCommit(tag)
	if err != nil {
		return err
	}
	if err := gr.DeleteTag(tag); err != nil {
		return err
	}
	wh, err := webhook.NewBranchTagEvent(ctx, l.actor.User, r, git.RefsTags+tag, commit.ID.String(), git.ZeroID)
	if err != nil {
		return err
	}
	return webhook.SendEvent(ctx, wh)
}

// Collaborators implements Service.
func (l *Local) Collaborators(ctx context.Context, name string) ([]Collaborator, error) {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return nil, err
	}
	names, err := l.be.Collaborators(ctx, r.Name())
	if err != nil {
		return nil, err
	}
	out := make([]Collaborator, 0, len(names))
	for _, n := range names {
		lvl, ok, err := l.be.IsCollaborator(ctx, r.Name(), n)
		if err != nil || !ok {
			continue
		}
		out = append(out, Collaborator{Username: n, Access: lvl.String()})
	}
	slices.SortFunc(out, func(a, b Collaborator) int { return strings.Compare(a.Username, b.Username) })
	return out, nil
}

// AddCollaborator implements Service.
func (l *Local) AddCollaborator(ctx context.Context, name, username, level string) error {
	ctx = l.withUser(ctx)
	r, caller, err := l.repo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	lvl := access.ReadWriteAccess
	if level != "" {
		lvl = access.ParseAccessLevel(level)
		if lvl < 0 {
			return access.ErrInvalidAccessLevel
		}
	}
	// Server admins may grant anything. Everyone else is bounded by their
	// own level, so a read-write collaborator cannot mint an admin.
	if !l.actor.Admin {
		if l.actor.User == nil {
			return proto.ErrUnauthorized
		}
		if lvl > caller {
			return proto.ErrExceedsAccessLevel
		}
	}
	if err := l.checkDemote(ctx, r.Name(), username, caller); err != nil {
		return err
	}
	return l.be.AddCollaborator(ctx, r.Name(), username, lvl)
}

// RemoveCollaborator implements Service.
func (l *Local) RemoveCollaborator(ctx context.Context, name, username string) error {
	ctx = l.withUser(ctx)
	r, caller, err := l.repo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	if err := l.checkDemote(ctx, r.Name(), username, caller); err != nil {
		return err
	}
	return l.be.RemoveCollaborator(ctx, r.Name(), username)
}

// checkDemote reports whether the actor may remove or overwrite an existing
// collaborator. Removal is privileged like granting: otherwise a read-write
// collaborator could remove an admin-access one and re-add them lower.
func (l *Local) checkDemote(ctx context.Context, repo, username string, caller access.AccessLevel) error {
	if l.actor.Admin {
		return nil
	}
	if l.actor.User == nil {
		return proto.ErrUnauthorized
	}
	current, isCollab, err := l.be.IsCollaborator(ctx, repo, username)
	if err != nil {
		// A missing row just means not a collaborator yet, the common case
		// when adding one. Anything else is real and fails closed.
		if errors.Is(err, db.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if isCollab && current > caller {
		return proto.ErrExceedsAccessLevel
	}
	return nil
}

func webhookOut(h webhook.Hook) Webhook {
	events := make([]string, len(h.Events))
	for i, e := range h.Events {
		events[i] = e.String()
	}
	return Webhook{
		ID:          h.ID,
		URL:         utils.Sanitize(h.URL),
		Events:      events,
		ContentType: contentTypeName(h.ContentType),
		Active:      h.Active,
		CreatedAt:   h.CreatedAt,
		UpdatedAt:   h.UpdatedAt,
	}
}

func parseEvents(names []string) ([]webhook.Event, error) {
	evs := make([]webhook.Event, 0, len(names))
	for _, n := range names {
		ev, err := webhook.ParseEvent(strings.TrimSpace(n))
		if err != nil {
			return nil, Invalid("invalid event: %v", err)
		}
		evs = append(evs, ev)
	}
	return evs, nil
}

// contentTypeName is the short name a content type is set by.
func contentTypeName(ct webhook.ContentType) string {
	if ct == webhook.ContentTypeForm {
		return "form"
	}
	return "json"
}

// parseContentType reads a content type by its short name, or its MIME
// type as webhook.ContentType.String gives it.
func parseContentType(s string) (webhook.ContentType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "json", webhook.ContentTypeJSON.String():
		return webhook.ContentTypeJSON, nil
	case "form", webhook.ContentTypeForm.String():
		return webhook.ContentTypeForm, nil
	default:
		return 0, webhook.ErrInvalidContentType
	}
}

// Webhooks implements Service.
func (l *Local) Webhooks(ctx context.Context, name string) ([]Webhook, error) {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.AdminAccess)
	if err != nil {
		return nil, err
	}
	hooks, err := l.be.ListWebhooks(ctx, r)
	if err != nil {
		return nil, err
	}
	out := make([]Webhook, len(hooks))
	for i, h := range hooks {
		out[i] = webhookOut(h)
	}
	return out, nil
}

// CreateWebhook implements Service.
func (l *Local) CreateWebhook(ctx context.Context, name string, opts WebhookOptions) (Webhook, error) {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.AdminAccess)
	if err != nil {
		return Webhook{}, err
	}
	evs, err := parseEvents(opts.Events)
	if err != nil {
		return Webhook{}, err
	}
	if opts.ContentType == "" {
		opts.ContentType = "json"
	}
	ct, err := parseContentType(opts.ContentType)
	if err != nil {
		return Webhook{}, err
	}
	active := opts.Active == nil || *opts.Active
	url := strings.TrimSpace(utils.Sanitize(opts.URL))

	before, err := l.be.ListWebhooks(ctx, r)
	if err != nil {
		return Webhook{}, err
	}
	if err := l.be.CreateWebhook(ctx, r, url, ct, opts.Secret, evs, active); err != nil {
		return Webhook{}, err
	}
	// The backend does not hand back what it created; it is the one hook
	// that was not there before.
	after, err := l.be.ListWebhooks(ctx, r)
	if err != nil {
		return Webhook{}, err
	}
	for _, h := range after {
		if !slices.ContainsFunc(before, func(b webhook.Hook) bool { return b.ID == h.ID }) {
			return webhookOut(h), nil
		}
	}
	return Webhook{}, errors.New("created webhook not found")
}

// UpdateWebhook implements Service.
func (l *Local) UpdateWebhook(ctx context.Context, name string, id int64, opts WebhookOptions) error {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.AdminAccess)
	if err != nil {
		return err
	}
	wh, err := l.be.Webhook(ctx, r, id)
	if err != nil {
		return err
	}
	url, secret, active, ct, events := wh.URL, wh.Secret, wh.Active, wh.ContentType, wh.Events
	if opts.URL != "" {
		url = strings.TrimSpace(utils.Sanitize(opts.URL))
	}
	if opts.Secret != "" {
		secret = opts.Secret
	}
	if opts.Active != nil {
		active = *opts.Active
	}
	if opts.ContentType != "" {
		if ct, err = parseContentType(opts.ContentType); err != nil {
			return err
		}
	}
	if len(opts.Events) > 0 {
		if events, err = parseEvents(opts.Events); err != nil {
			return err
		}
	}
	return l.be.UpdateWebhook(ctx, r, id, url, ct, secret, events, active)
}

// DeleteWebhook implements Service.
func (l *Local) DeleteWebhook(ctx context.Context, name string, id int64) error {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.AdminAccess)
	if err != nil {
		return err
	}
	return l.be.DeleteWebhook(ctx, r, id)
}

func deliveryOut(d webhook.Delivery) Delivery {
	return Delivery{
		ID:             d.ID.String(),
		Event:          d.Event.String(),
		ResponseStatus: d.ResponseStatus,
		CreatedAt:      d.CreatedAt,
	}
}

// Deliveries implements Service.
func (l *Local) Deliveries(ctx context.Context, name string, id int64) ([]Delivery, error) {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.AdminAccess)
	if err != nil {
		return nil, err
	}
	dels, err := l.be.ListWebhookDeliveries(ctx, r, id)
	if err != nil {
		return nil, err
	}
	out := make([]Delivery, len(dels))
	for i, d := range dels {
		out[i] = deliveryOut(d)
	}
	return out, nil
}

func parseDeliveryID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}, Invalid("invalid delivery ID: %v", err)
	}
	return id, nil
}

// Delivery implements Service.
func (l *Local) Delivery(ctx context.Context, name string, id int64, deliveryID string) (DeliveryDetail, error) {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.AdminAccess)
	if err != nil {
		return DeliveryDetail{}, err
	}
	did, err := parseDeliveryID(deliveryID)
	if err != nil {
		return DeliveryDetail{}, err
	}
	d, err := l.be.WebhookDelivery(ctx, r, id, did)
	if err != nil {
		return DeliveryDetail{}, err
	}
	return DeliveryDetail{
		Delivery:        deliveryOut(d),
		RequestURL:      d.RequestURL,
		RequestMethod:   d.RequestMethod,
		RequestError:    d.RequestError.String,
		RequestHeaders:  d.RequestHeaders,
		RequestBody:     d.RequestBody,
		ResponseHeaders: d.ResponseHeaders,
		ResponseBody:    d.ResponseBody,
	}, nil
}

// Redeliver implements Service.
func (l *Local) Redeliver(ctx context.Context, name string, id int64, deliveryID string) error {
	ctx = l.withUser(ctx)
	r, _, err := l.repo(ctx, name, access.AdminAccess)
	if err != nil {
		return err
	}
	did, err := parseDeliveryID(deliveryID)
	if err != nil {
		return err
	}
	return l.be.RedeliverWebhookDelivery(ctx, r, id, did)
}
