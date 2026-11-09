package ops

import (
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/proto"
)

func (f fixture) root(t *testing.T) (proto.User, *Local) {
	t.Helper()
	u, err := f.be.CreateUser(f.ctx, "root", proto.UserOptions{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	return u, NewLocal(f.be, UserActor(u))
}

func TestAdminOnly(t *testing.T) {
	f := newFixture(t)
	for name, l := range map[string]*Local{"anonymous": f.as(nil), "repo admin": f.as(f.admin)} {
		_, err := l.Users(f.ctx)
		wantErr(t, err, proto.ErrUnauthorized)
		// Asking about a user who doesn't exist says the same, so
		// non-admins can't find out who has an account.
		_, err = l.User(f.ctx, "ghost")
		wantErr(t, err, proto.ErrUnauthorized)
		_, err = l.CreateUser(f.ctx, CreateUser{Username: "x"})
		wantErr(t, err, proto.ErrUnauthorized)
		wantErr(t, l.UpdateUser(f.ctx, "reader", UserUpdate{Admin: ptr(true)}), proto.ErrUnauthorized)
		_, err = l.AddUserKey(f.ctx, "reader", keyA)
		wantErr(t, err, proto.ErrUnauthorized)
		_, err = l.Settings(f.ctx)
		wantErr(t, err, proto.ErrUnauthorized)
		_, err = l.UpdateSettings(f.ctx, SettingsUpdate{AllowKeyless: ptr(false)})
		wantErr(t, err, proto.ErrUnauthorized)
		if t.Failed() {
			t.Fatalf("as %s", name)
		}
	}
}

func TestManageUsers(t *testing.T) {
	f := newFixture(t)
	_, a := f.root(t)

	u, err := a.CreateUser(f.ctx, CreateUser{Username: "Nova", Keys: []string{keyA}})
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "nova" || u.Admin || len(u.PublicKeys) != 1 {
		t.Errorf("created %+v", u)
	}
	_, err = a.CreateUser(f.ctx, CreateUser{Username: "nova"})
	wantErr(t, err, proto.ErrUserExist)
	_, err = a.CreateUser(f.ctx, CreateUser{Username: "other", Keys: []string{keyA}})
	wantErr(t, err, proto.ErrPublicKeyInUse)
	if _, err := a.CreateUser(f.ctx, CreateUser{Username: "9lives"}); !IsInvalid(err) {
		t.Errorf("bad name: err = %v", err)
	}
	if _, err := a.CreateUser(f.ctx, CreateUser{Username: "x", Keys: []string{"nope"}}); !IsInvalid(err) {
		t.Errorf("bad key: err = %v", err)
	}
	_, err = a.User(f.ctx, "ghost")
	wantErr(t, err, proto.ErrUserNotFound)

	us, err := a.Users(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(us) != 8 || us[slicesIndex(us, "nova")].Admin || !us[slicesIndex(us, "root")].Admin || us[0].Username != "admin" {
		t.Errorf("users = %+v", us)
	}

	if err := a.UpdateUser(f.ctx, "nova", UserUpdate{Admin: ptr(true), Username: ptr("nova2")}); err != nil {
		t.Fatal(err)
	}
	u, err = a.User(f.ctx, "nova2")
	if err != nil || !u.Admin {
		t.Errorf("after update = %+v, %v", u, err)
	}
	wantErr(t, a.UpdateUser(f.ctx, "nova2", UserUpdate{Username: ptr("reader")}), proto.ErrUserExist)

	if _, err := a.AddUserKey(f.ctx, "nova2", keyB); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveUserKey(f.ctx, "nova2", keyA); err != nil {
		t.Fatal(err)
	}
	wantErr(t, a.RemoveUserKey(f.ctx, "nova2", keyA), proto.ErrPublicKeyNotFound)

	// Owners of repositories must deal with them first.
	wantErr(t, a.DeleteUser(f.ctx, "owner"), proto.ErrUserOwnsRepos)
	if err := a.DeleteUser(f.ctx, "nova2"); err != nil {
		t.Fatal(err)
	}
	wantErr(t, a.DeleteUser(f.ctx, "nova2"), proto.ErrUserNotFound)
}

func slicesIndex(us []UserSummary, name string) int {
	for i, u := range us {
		if u.Username == name {
			return i
		}
	}
	return 0
}

func TestAdminCannotLockThemselvesOut(t *testing.T) {
	f := newFixture(t)
	_, a := f.root(t)

	if err := a.DeleteUser(f.ctx, "root"); !IsInvalid(err) {
		t.Errorf("delete self: err = %v", err)
	}
	if err := a.UpdateUser(f.ctx, "root", UserUpdate{Admin: ptr(false)}); !IsInvalid(err) {
		t.Errorf("demote self: err = %v", err)
	}
	// Saying what is already so is fine.
	if err := a.UpdateUser(f.ctx, "root", UserUpdate{Admin: ptr(true)}); err != nil {
		t.Errorf("re-affirm admin: %v", err)
	}
}

func TestServerSettings(t *testing.T) {
	f := newFixture(t)
	_, a := f.root(t)

	s, err := a.UpdateSettings(f.ctx, SettingsUpdate{AnonAccess: ptr("no-access"), AllowKeyless: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if s.AnonAccess != "no-access" || s.AllowKeyless || s.AnonAccessFixed {
		t.Errorf("settings = %+v", s)
	}
	if _, err := a.UpdateSettings(f.ctx, SettingsUpdate{AnonAccess: ptr("everything")}); !IsInvalid(err) {
		t.Errorf("bad level: err = %v", err)
	}
}
