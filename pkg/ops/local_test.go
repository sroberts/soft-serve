package ops

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/charmbracelet/soft-serve/pkg/db/migrate"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/store"
	"github.com/charmbracelet/soft-serve/pkg/store/database"
)

type fixture struct {
	ctx                                    context.Context
	be                                     *backend.Backend
	owner, reader, writer, admin, stranger proto.User
}

// newFixture is a server with a private repository "secret" owned by owner,
// a public hidden one "shy", and collaborators on "secret" at each level.
func newFixture(t *testing.T) fixture {
	t.Helper()
	dp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataPath = dp
	cfg.DB.Driver = "sqlite"
	cfg.DB.DataSource = dp + "/test.db"

	ctx := config.WithContext(context.Background(), cfg)
	dbx, err := db.Open(ctx, cfg.DB.Driver, cfg.DB.DataSource)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbx.Close() }) //nolint:errcheck
	if err := migrate.Migrate(ctx, dbx); err != nil {
		t.Fatal(err)
	}
	st := database.New(ctx, dbx)
	ctx = db.WithContext(ctx, dbx)
	ctx = store.WithContext(ctx, st)
	be := backend.New(ctx, cfg, dbx, st)
	ctx = backend.WithContext(ctx, be)

	f := fixture{ctx: ctx, be: be}
	mk := func(name string) proto.User {
		u, err := be.CreateUser(ctx, name, proto.UserOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	f.owner, f.reader, f.writer, f.admin, f.stranger = mk("owner"), mk("reader"), mk("writer"), mk("repoadmin"), mk("stranger")

	if _, err := be.CreateRepository(ctx, "secret", f.owner, proto.RepositoryOptions{Private: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := be.CreateRepository(ctx, "shy", f.owner, proto.RepositoryOptions{Hidden: true}); err != nil {
		t.Fatal(err)
	}
	for u, lvl := range map[proto.User]access.AccessLevel{
		f.reader: access.ReadOnlyAccess,
		f.writer: access.ReadWriteAccess,
		f.admin:  access.AdminAccess,
	} {
		if err := be.AddCollaborator(proto.WithUserContext(ctx, f.owner), "secret", u.Username(), lvl); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f fixture) as(u proto.User) *Local {
	return NewLocal(f.be, Actor{User: u})
}

func ptr[T any](v T) *T { return &v }

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestUnreadableLooksMissing(t *testing.T) {
	f := newFixture(t)

	// A stranger cannot tell a private repository from a missing one.
	_, err := f.as(f.stranger).Repo(f.ctx, "secret")
	wantErr(t, err, proto.ErrRepoNotFound)
	_, err = f.as(f.stranger).Repo(f.ctx, "ghost")
	wantErr(t, err, proto.ErrRepoNotFound)

	// Anonymous callers are told to authenticate, either way.
	_, err = f.as(nil).Repo(f.ctx, "secret")
	wantErr(t, err, proto.ErrUnauthorized)

	// Changing it fails the same way, before any write check.
	wantErr(t, f.as(f.stranger).UpdateRepo(f.ctx, "secret", RepoUpdate{Description: ptr("x")}), proto.ErrRepoNotFound)
	wantErr(t, f.as(f.stranger).DeleteRepo(f.ctx, "secret"), proto.ErrRepoNotFound)
}

func TestWriteNeedsReadWrite(t *testing.T) {
	f := newFixture(t)

	r, err := f.as(f.reader).Repo(f.ctx, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if r.Access != "read-only" {
		t.Errorf("reader access = %q", r.Access)
	}
	wantErr(t, f.as(f.reader).UpdateRepo(f.ctx, "secret", RepoUpdate{Description: ptr("x")}), proto.ErrUnauthorized)
	wantErr(t, f.as(f.reader).SyncMirror(f.ctx, "secret"), proto.ErrUnauthorized)
	_, err = f.as(f.reader).Collaborators(f.ctx, "secret")
	wantErr(t, err, proto.ErrUnauthorized)

	if err := f.as(f.writer).UpdateRepo(f.ctx, "secret", RepoUpdate{Description: ptr("new")}); err != nil {
		t.Fatal(err)
	}
	r, _ = f.as(f.writer).Repo(f.ctx, "secret")
	if r.Description != "new" {
		t.Errorf("description = %q", r.Description)
	}

	// A sync of something that is not a mirror says so.
	wantErr(t, f.as(f.writer).SyncMirror(f.ctx, "secret"), proto.ErrNotMirror)
	wantErr(t, f.as(f.writer).UpdateRepo(f.ctx, "secret", RepoUpdate{AutoSync: ptr(false)}), proto.ErrNotMirror)
}

func TestCollaboratorEscalation(t *testing.T) {
	f := newFixture(t)
	w := f.as(f.writer)

	// A read-write collaborator cannot mint an admin, or touch one.
	wantErr(t, w.AddCollaborator(f.ctx, "secret", "stranger", "admin-access"), proto.ErrExceedsAccessLevel)
	wantErr(t, w.RemoveCollaborator(f.ctx, "secret", "repoadmin"), proto.ErrExceedsAccessLevel)
	wantErr(t, w.AddCollaborator(f.ctx, "secret", "repoadmin", "read-only"), proto.ErrExceedsAccessLevel)
	wantErr(t, w.AddCollaborator(f.ctx, "secret", "stranger", "bogus"), access.ErrInvalidAccessLevel)

	// They can add someone at their own level, which is the default, but
	// adding someone twice is refused rather than taken as a change.
	if err := w.AddCollaborator(f.ctx, "secret", "stranger", ""); err != nil {
		t.Fatal(err)
	}
	wantErr(t, w.AddCollaborator(f.ctx, "secret", "stranger", "read-only"), proto.ErrCollaboratorExist)
	cs, err := w.Collaborators(f.ctx, "secret")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range cs {
		if c.Username == "stranger" {
			found = c.Access == "read-write"
		}
	}
	if !found {
		t.Errorf("stranger not added at read-write: %v", cs)
	}

	// A server admin is bound by none of it.
	root, err := f.be.CreateUser(f.ctx, "root", proto.UserOptions{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	sa := NewLocal(f.be, UserActor(root))
	if err := sa.RemoveCollaborator(f.ctx, "secret", "repoadmin"); err != nil {
		t.Fatal(err)
	}
	if err := sa.AddCollaborator(f.ctx, "secret", "stranger2", "admin-access"); err == nil {
		t.Error("added a collaborator who has no account")
	}
}

func TestWebhooksNeedAdmin(t *testing.T) {
	f := newFixture(t)

	_, err := f.as(f.writer).Webhooks(f.ctx, "secret")
	wantErr(t, err, proto.ErrUnauthorized)

	a := f.as(f.admin)
	wh, err := a.CreateWebhook(f.ctx, "secret", WebhookOptions{
		URL:    "https://example.com/hook",
		Events: []string{"push"},
		Secret: "s3cret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if wh.ID == 0 || wh.ContentType != "json" || !wh.Active {
		t.Errorf("created webhook = %+v", wh)
	}

	if err := a.UpdateWebhook(f.ctx, "secret", wh.ID, WebhookOptions{Active: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	hooks, err := a.Webhooks(f.ctx, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 || hooks[0].Active || hooks[0].URL != "https://example.com/hook" {
		t.Errorf("after update = %+v", hooks)
	}

	_, err = a.CreateWebhook(f.ctx, "secret", WebhookOptions{URL: "https://example.com/x", Events: []string{"nope"}})
	if !IsInvalid(err) {
		t.Errorf("bad event: err = %v, want InvalidError", err)
	}
	_, err = a.Delivery(f.ctx, "secret", wh.ID, "not-a-uuid")
	if !IsInvalid(err) {
		t.Errorf("bad delivery id: err = %v, want InvalidError", err)
	}

	if err := a.DeleteWebhook(f.ctx, "secret", wh.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndRename(t *testing.T) {
	f := newFixture(t)
	w := f.as(f.writer)

	// Import-only options make no sense without a remote.
	_, err := w.CreateRepo(f.ctx, CreateRepo{Name: "x", AutoSync: ptr(false)})
	if !IsInvalid(err) {
		t.Errorf("auto_sync without remote: err = %v", err)
	}
	_, err = w.CreateRepo(f.ctx, CreateRepo{Name: "x", Remote: "https://example.com/x.git", AutoSync: ptr(false)})
	if !IsInvalid(err) {
		t.Errorf("auto_sync without mirror: err = %v", err)
	}

	r, err := w.CreateRepo(f.ctx, CreateRepo{Name: "mine", Description: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "mine" || r.Owner != "writer" || r.Description != "d" {
		t.Errorf("created = %+v", r)
	}

	// Renaming onto a repository the caller cannot read is refused
	// without saying it exists.
	wantErr(t, f.as(f.stranger).RenameRepo(f.ctx, "shy", "secret"), proto.ErrUnauthorized)
	if err := w.RenameRepo(f.ctx, "mine", "ours"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Repo(f.ctx, "ours"); err != nil {
		t.Fatal(err)
	}
}

func TestListRepos(t *testing.T) {
	f := newFixture(t)

	names := func(rs []RepoSummary) map[string]bool {
		m := map[string]bool{}
		for _, r := range rs {
			m[r.Name] = true
		}
		return m
	}

	rs, err := f.as(f.stranger).ListRepos(f.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(rs); got["secret"] || got["shy"] {
		t.Errorf("stranger sees %v", got)
	}
	rs, _ = f.as(f.stranger).ListRepos(f.ctx, true)
	if got := names(rs); got["secret"] || !got["shy"] {
		t.Errorf("stranger with all sees %v", got)
	}
	rs, _ = f.as(f.reader).ListRepos(f.ctx, false)
	if got := names(rs); !got["secret"] {
		t.Errorf("reader sees %v", got)
	}
}
