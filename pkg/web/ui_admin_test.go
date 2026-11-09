package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/config"
)

func TestUIAdminRefused(t *testing.T) {
	f := newViewFixture(t)
	key, _ := newKey(t)
	gets := []string{
		"/-/admin", "/-/admin/users", "/-/admin/settings", "/-/admin/nope",
		"/-/admin/users/stranger", "/-/admin/users/ghost",
		"/-/admin/users/stranger/delete", "/-/admin/users/ghost/delete",
		"/-/admin/users/stranger/keys", "/-/admin/users/ghost/keys",
	}
	posts := map[string]url.Values{
		"/-/admin/users":                      {"username": {"newbie"}},
		"/-/admin/users/stranger/keys":        {"key": {key}},
		"/-/admin/users/ghost/keys":           {"key": {key}},
		"/-/admin/users/stranger/keys/remove": {"fingerprint": {"SHA256:x"}},
		"/-/admin/users/ghost/keys/remove":    {"fingerprint": {"SHA256:x"}},
		"/-/admin/users/owner/admin":          {"admin": {"true"}},
		"/-/admin/users/ghost/admin":          {"admin": {"true"}},
		"/-/admin/users/stranger/rename":      {"username": {"other"}},
		"/-/admin/users/ghost/rename":         {"username": {"other"}},
		"/-/admin/users/stranger/delete":      {},
		"/-/admin/users/ghost/delete":         {},
		"/-/admin/settings":                   {"anon_access": {"admin-access"}},
	}
	for _, c := range []struct {
		who, auth string
		want      int
	}{{"anonymous", "", http.StatusUnauthorized}, {"owner", f.owner(), http.StatusForbidden}} {
		for _, p := range gets {
			w := f.do(req{path: p, auth: c.auth})
			wantStatus(t, w, c.want, c.who+" GET "+p)
			hasNot(t, w, "stranger", "ghost", `href="/-/admin"`)
		}
		for p, form := range posts {
			w := f.post(p, c.auth, form)
			wantStatus(t, w, c.want, c.who+" POST "+p)
		}
	}
	// Nothing changed.
	if _, err := f.be.User(f.ctx, "newbie"); err == nil {
		t.Error("a non-admin created a user")
	}
	if u, _ := f.be.User(f.ctx, "owner"); u.IsAdmin() {
		t.Error("a non-admin made themselves an admin")
	}
	if _, err := f.be.User(f.ctx, "stranger"); err != nil {
		t.Error("a non-admin deleted or renamed a user")
	}
	if f.be.AnonAccess(f.ctx) == access.AdminAccess {
		t.Error("a non-admin changed anon-access")
	}

	// An Outpost user with no account here, and one who isn't an admin,
	// are refused the same way.
	nobody := f.outpost.token(t, "nobody", viewHost, nil)
	wantStatus(t, f.do(req{path: "/-/admin/users", identity: nobody}), http.StatusUnauthorized, "outpost user without an account")
	owner := f.outpost.token(t, "owner", viewHost, nil)
	wantStatus(t, f.do(req{path: "/-/admin/users/ghost", identity: owner}), http.StatusForbidden, "outpost non-admin")

	// Only admins see the link.
	hasNot(t, f.do(req{path: "/", auth: f.owner()}), `href="/-/admin"`)
	has(t, f.do(req{path: "/", auth: f.adminAuth(t)}), `href="/-/admin"`)
}

func TestUIAdminUsers(t *testing.T) {
	f := newViewFixture(t)
	auth := f.adminAuth(t)

	seeOther(t, f.do(req{path: "/-/admin", auth: auth}), "/-/admin/users")
	w := f.do(req{path: "/-/admin/users", auth: auth})
	has(t, w, `href="/-/admin/users/owner"`, `href="/-/admin/users/stranger"`, `href="/-/admin/users/admin"`, `<span class="badge">admin</span>`, `action="/-/admin/users"`)

	// Create, with a key and the admin flag.
	key, fp := newKey(t)
	loc := seeOther(t, f.post("/-/admin/users", auth, url.Values{"username": {"Nova"}, "admin": {"on"}, "key": {key}}), "/-/admin/users/nova?done=user-created")
	w = f.do(req{path: loc, auth: auth})
	has(t, w, "User created.", esc(fp), "Remove admin")
	u, err := f.be.User(f.ctx, "nova")
	if err != nil || !u.IsAdmin() || len(u.PublicKeys()) != 1 {
		t.Fatalf("nova = %v, %v", u, err)
	}

	// Without a key or the flag.
	seeOther(t, f.post("/-/admin/users", auth, url.Values{"username": {"plain"}, "key": {"  "}}), "/-/admin/users/plain?done=user-created")
	if u, err := f.be.User(f.ctx, "plain"); err != nil || u.IsAdmin() || len(u.PublicKeys()) != 0 {
		t.Errorf("plain = %v, %v", u, err)
	}

	// A duplicate, a bad name and a bad key fail on the form, which keeps
	// what was typed.
	w = f.post("/-/admin/users", auth, url.Values{"username": {"nova"}})
	wantStatus(t, w, http.StatusConflict, "duplicate")
	has(t, w, "Username is already taken.", `name="username" value="nova"`, `class="formerror"`)
	w = f.post("/-/admin/users", auth, url.Values{"username": {"9lives"}, "admin": {"on"}})
	wantStatus(t, w, http.StatusBadRequest, "bad name")
	has(t, w, "Username must start with a letter.", `value="9lives"`, `name="admin" checked`)
	w = f.post("/-/admin/users", auth, url.Values{"username": {"keyed"}, "key": {"not a key"}})
	wantStatus(t, w, http.StatusBadRequest, "bad key")
	has(t, w, "Not an SSH public key", ">not a key</textarea>")
	if _, err := f.be.User(f.ctx, "keyed"); err == nil {
		t.Error("created a user with a bad key")
	}

	// A user who doesn't exist is not found, for an admin.
	wantStatus(t, f.do(req{path: "/-/admin/users/ghost", auth: auth}), http.StatusNotFound, "ghost")
	wantStatus(t, f.post("/-/admin/users/ghost/rename", auth, url.Values{"username": {"x"}}), http.StatusNotFound, "rename ghost")
	wantStatus(t, f.do(req{path: "/-/admin/users/owner/nope", auth: auth}), http.StatusNotFound, "unknown action")
	// A GET to a form's address goes back to the page.
	seeOther(t, f.do(req{path: "/-/admin/users/owner/keys", auth: auth}), "/-/admin/users/owner")
}

func TestUIAdminUserKeys(t *testing.T) {
	f := newViewFixture(t)
	auth := f.adminAuth(t)
	key, fp := newKey(t)

	has(t, f.do(req{path: "/-/admin/users/stranger", auth: auth}), "No SSH keys.", `action="/-/admin/users/stranger/keys"`)
	seeOther(t, f.post("/-/admin/users/stranger/keys", auth, url.Values{"key": {key}}), "/-/admin/users/stranger?done=key-added")
	w := f.do(req{path: "/-/admin/users/stranger?done=key-added", auth: auth})
	has(t, w, "SSH key added.", `name="fingerprint" value="`+esc(fp)+`"`)

	w = f.post("/-/admin/users/stranger/keys", auth, url.Values{"key": {key}})
	wantStatus(t, w, http.StatusConflict, "same key again")
	has(t, w, "They already have this key.", ">"+esc(key)+"</textarea>")
	w = f.post("/-/admin/users/owner/keys", auth, url.Values{"key": {key}})
	wantStatus(t, w, http.StatusConflict, "someone else's key")
	has(t, w, "Public key belongs to another account.")

	seeOther(t, f.post("/-/admin/users/stranger/keys/remove", auth, url.Values{"fingerprint": {fp}}), "/-/admin/users/stranger?done=key-removed")
	if u, _ := f.be.User(f.ctx, "stranger"); len(u.PublicKeys()) != 0 {
		t.Error("key not removed")
	}
	w = f.post("/-/admin/users/stranger/keys/remove", auth, url.Values{"fingerprint": {fp}})
	wantStatus(t, w, http.StatusNotFound, "remove twice")
	has(t, w, esc("That key isn't on this account."))
}

func TestUIAdminSetAdminAndRename(t *testing.T) {
	f := newViewFixture(t)
	auth := f.adminAuth(t)

	has(t, f.do(req{path: "/-/admin/users/stranger", auth: auth}), "Make admin", `name="admin" value="true"`)
	seeOther(t, f.post("/-/admin/users/stranger/admin", auth, url.Values{"admin": {"true"}}), "/-/admin/users/stranger?done=admin-granted")
	if u, _ := f.be.User(f.ctx, "stranger"); !u.IsAdmin() {
		t.Error("not made an admin")
	}
	has(t, f.do(req{path: "/-/admin/users/stranger?done=admin-granted", auth: auth}), esc("They're an admin now."), `name="admin" value="false"`)
	seeOther(t, f.post("/-/admin/users/stranger/admin", auth, url.Values{"admin": {"false"}}), "/-/admin/users/stranger?done=admin-revoked")
	if u, _ := f.be.User(f.ctx, "stranger"); u.IsAdmin() {
		t.Error("still an admin")
	}
	wantStatus(t, f.post("/-/admin/users/stranger/admin", auth, url.Values{"admin": {"maybe"}}), http.StatusBadRequest, "bad admin value")

	// Rename.
	seeOther(t, f.post("/-/admin/users/stranger/rename", auth, url.Values{"username": {"Wanderer"}}), "/-/admin/users/wanderer?done=user-renamed")
	if _, err := f.be.User(f.ctx, "wanderer"); err != nil {
		t.Errorf("not renamed: %v", err)
	}
	w := f.post("/-/admin/users/wanderer/rename", auth, url.Values{"username": {"owner"}})
	wantStatus(t, w, http.StatusConflict, "taken name")
	has(t, w, "Username is already taken.", `name="username" value="owner"`)
	w = f.post("/-/admin/users/wanderer/rename", auth, url.Values{"username": {"no spaces"}})
	wantStatus(t, w, http.StatusBadRequest, "bad name")

	// Behind Outpost, the rename form says what a rename does to sign-in.
	has(t, f.do(req{path: "/-/admin/users/wanderer", auth: auth}), "Outpost login named wanderer no longer reaches this account")
	plain := *f
	plain.handler = newRouter(f.ctx, nil)
	pw := plain.do(req{path: "/-/admin/users/wanderer", auth: auth})
	wantStatus(t, pw, http.StatusOK, "without outpost")
	hasNot(t, pw, "Outpost")
}

func TestUIAdminDelete(t *testing.T) {
	f := newViewFixture(t)
	auth := f.adminAuth(t)

	// Deleting asks first.
	w := f.do(req{path: "/-/admin/users/stranger/delete", auth: auth})
	has(t, w, "Delete this user?", `action="/-/admin/users/stranger/delete"`, "Delete user")
	if _, err := f.be.User(f.ctx, "stranger"); err != nil {
		t.Fatal("deleted on GET")
	}
	wantStatus(t, f.do(req{path: "/-/admin/users/ghost/delete", auth: auth}), http.StatusNotFound, "confirm ghost")
	seeOther(t, f.post("/-/admin/users/stranger/delete", auth, url.Values{}), "/-/admin/users?done=user-deleted")
	if _, err := f.be.User(f.ctx, "stranger"); err == nil {
		t.Error("not deleted")
	}
	has(t, f.do(req{path: "/-/admin/users?done=user-deleted", auth: auth}), "User deleted.")

	// Someone who owns repositories can't be, and the page says which.
	w = f.post("/-/admin/users/owner/delete", auth, url.Values{})
	wantStatus(t, w, http.StatusConflict, "repo owner")
	has(t, w, "still own these repositories", `href="/group/open"`, `href="/secret"`)
	if _, err := f.be.User(f.ctx, "owner"); err != nil {
		t.Error("deleted a repo owner")
	}
}

func TestUIAdminSelf(t *testing.T) {
	f := newViewFixture(t)
	auth := f.adminAuth(t)

	// Not offered on their own page, with why.
	w := f.do(req{path: "/-/admin/users/admin", auth: auth})
	has(t, w, "You can't delete your own account", "You can't remove your own admin rights")
	hasNot(t, w, `href="/-/admin/users/admin/delete"`, `name="admin" value="false"`)
	has(t, f.do(req{path: "/-/admin/users", auth: auth}), `<span class="muted small">you</span>`)

	// Refused if asked anyway.
	wantStatus(t, f.do(req{path: "/-/admin/users/admin/delete", auth: auth}), http.StatusBadRequest, "confirm self-delete")
	w = f.post("/-/admin/users/admin/delete", auth, url.Values{})
	wantStatus(t, w, http.StatusBadRequest, "self-delete")
	has(t, w, esc("You can't delete your own account"))
	w = f.post("/-/admin/users/admin/admin", auth, url.Values{"admin": {"false"}})
	wantStatus(t, w, http.StatusBadRequest, "self-demote")
	has(t, w, esc("You can't remove your own admin rights"))
	u, err := f.be.User(f.ctx, "admin")
	if err != nil || !u.IsAdmin() {
		t.Errorf("admin = %v, %v", u, err)
	}
}

func TestUIAdminSettings(t *testing.T) {
	f := newViewFixture(t)
	auth := f.adminAuth(t)

	w := f.do(req{path: "/-/admin/settings", auth: auth})
	has(t, w, `<select name="anon_access">`, `value="no-access"`, `value="admin-access"`, `name="allow_keyless"`)

	seeOther(t, f.post("/-/admin/settings", auth, url.Values{"anon_access": {"no-access"}}), "/-/admin/settings?done=saved")
	if got := f.be.AnonAccess(f.ctx); got != access.NoAccess {
		t.Errorf("anon-access = %v", got)
	}
	if f.be.AllowKeyless(f.ctx) {
		t.Error("allow-keyless still on")
	}
	w = f.do(req{path: "/-/admin/settings?done=saved", auth: auth})
	has(t, w, "Settings saved.", `<option value="no-access" selected>`)
	hasNot(t, w, `name="allow_keyless" checked`)

	seeOther(t, f.post("/-/admin/settings", auth, url.Values{"anon_access": {"read-only"}, "allow_keyless": {"on"}}), "/-/admin/settings?done=saved")
	if f.be.AnonAccess(f.ctx) != access.ReadOnlyAccess || !f.be.AllowKeyless(f.ctx) {
		t.Error("settings not changed back")
	}

	w = f.post("/-/admin/settings", auth, url.Values{"anon_access": {"lots"}, "allow_keyless": {"on"}})
	wantStatus(t, w, http.StatusBadRequest, "bad level")
	has(t, w, "Choose one of the access levels offered.")
	if f.be.AnonAccess(f.ctx) != access.ReadOnlyAccess {
		t.Error("bad level changed anon-access")
	}

	// A value the config file sets is said to be fixed.
	hasNot(t, f.do(req{path: "/-/admin/settings", auth: auth}), "Set in the server")
	cfg := config.FromContext(f.ctx)
	level, keyless := access.ReadOnlyAccess, true
	cfg.AnonAccess, cfg.AllowKeyless = &level, &keyless
	t.Cleanup(func() { cfg.AnonAccess, cfg.AllowKeyless = nil, nil })
	w = f.do(req{path: "/-/admin/settings", auth: auth})
	if n := strings.Count(w.Body.String(), "Set in the server's config file"); n != 2 {
		t.Errorf("%d fixed notes, want 2", n)
	}
}
