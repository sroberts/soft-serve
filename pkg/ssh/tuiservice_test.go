package ssh

import (
	"context"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/charmbracelet/soft-serve/pkg/db/migrate"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/store"
	"github.com/charmbracelet/soft-serve/pkg/store/database"
	_ "modernc.org/sqlite" // sqlite driver
)

// A TUI session that renames its user keeps its access to the
// repositories the user collaborates on, and its context follows.
func TestSessionServiceFollowsRename(t *testing.T) {
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

	owner, err := be.CreateUser(ctx, "owner", proto.UserOptions{})
	if err != nil {
		t.Fatal(err)
	}
	user, err := be.CreateUser(ctx, "collab", proto.UserOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.CreateRepository(ctx, "secret", owner, proto.RepositoryOptions{Private: true}); err != nil {
		t.Fatal(err)
	}
	if err := be.AddCollaborator(proto.WithUserContext(ctx, owner), "secret", "collab", access.ReadOnlyAccess); err != nil {
		t.Fatal(err)
	}

	var ctxUser proto.User
	svc := newSessionService(be, ops.UserActor(user), func(u proto.User) { ctxUser = u })
	stale := ops.NewLocal(be, ops.UserActor(user))

	if _, err := svc.Repo(ctx, "secret"); err != nil {
		t.Fatalf("before rename: %v", err)
	}
	if err := svc.SetUsername(ctx, "renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Repo(ctx, "secret"); err != nil {
		t.Errorf("lost access after rename: %v", err)
	}
	if ctxUser == nil || ctxUser.Username() != "renamed" {
		t.Errorf("context user = %v, want renamed", ctxUser)
	}
	// Without the refresh, the session would have lost its access.
	if _, err := stale.Repo(ctx, "secret"); err == nil {
		t.Error("a stale actor still reads the repository; the test no longer shows the problem")
	}

	// A failed rename leaves the session as it was.
	ctxUser = nil
	if err := svc.SetUsername(ctx, "owner"); err == nil {
		t.Error("took another user's name")
	}
	if ctxUser != nil {
		t.Error("refreshed after a failed rename")
	}
}
