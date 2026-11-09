package web

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/charmbracelet/soft-serve/pkg/ops"
)

// adminAuth makes the fixture's admin user a token.
func (f *viewFixture) adminAuth(t *testing.T) string {
	t.Helper()
	u, err := f.be.User(f.ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := f.be.CreateAccessToken(f.ctx, u, "admin", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return "Token " + tok
}

func TestAPIAdmin(t *testing.T) {
	f := newViewFixture(t)
	auth := f.adminAuth(t)

	// Non-admins learn nothing, not even whether a user exists.
	for _, c := range []struct {
		auth string
		want int
	}{{"", http.StatusUnauthorized}, {f.owner(), http.StatusForbidden}} {
		for _, r := range []req{
			{path: "/v1/users"},
			{path: "/v1/users/stranger"},
			{path: "/v1/users/ghost"},
			{method: http.MethodPost, path: "/v1/users", body: `{"username":"x"}`},
			{method: http.MethodPatch, path: "/v1/users/owner", body: `{"admin":true}`},
			{method: http.MethodDelete, path: "/v1/users/stranger"},
			{path: "/v1/settings"},
			{method: http.MethodPatch, path: "/v1/settings", body: `{"anon_access":"admin-access"}`},
		} {
			r.auth = c.auth
			if w := f.do(r); w.Code != c.want {
				t.Errorf("%q %s %s: %d %s", c.auth, r.method, r.path, w.Code, w.Body)
			}
		}
	}

	w := f.do(req{method: http.MethodPost, path: "/v1/users", auth: auth, body: `{"username":"nova","keys":["` + testKey + `"]}`})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodPost, path: "/v1/users", auth: auth, body: `{"username":"nova"}`}); w.Code != http.StatusConflict {
		t.Errorf("create again: %d %s", w.Code, w.Body)
	}
	us := decodeJSON[[]ops.UserSummary](t, f.do(req{path: "/v1/users", auth: auth}).Body.Bytes())
	if len(us) != 4 {
		t.Errorf("users = %+v", us)
	}
	if w := f.do(req{path: "/v1/users/ghost", auth: auth}); w.Code != http.StatusNotFound {
		t.Errorf("ghost: %d %s", w.Code, w.Body)
	}

	if w := f.do(req{method: http.MethodPatch, path: "/v1/users/nova", auth: auth, body: `{"admin":true,"username":"nova2"}`}); w.Code != http.StatusNoContent {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	u := decodeJSON[ops.Account](t, f.do(req{path: "/v1/users/nova2", auth: auth}).Body.Bytes())
	if !u.Admin || len(u.PublicKeys) != 1 {
		t.Errorf("nova2 = %+v", u)
	}
	del := "/v1/users/nova2/keys?key=" + url.QueryEscape(u.PublicKeys[0].Fingerprint)
	if w := f.do(req{method: http.MethodDelete, path: del, auth: auth}); w.Code != http.StatusNoContent {
		t.Errorf("remove key: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodPost, path: "/v1/users/nova2/keys", auth: auth, body: `{"key":"` + testKey + `"}`}); w.Code != http.StatusCreated {
		t.Errorf("add key: %d %s", w.Code, w.Body)
	}

	if w := f.do(req{method: http.MethodDelete, path: "/v1/users/owner", auth: auth}); w.Code != http.StatusConflict {
		t.Errorf("delete owner of repos: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodDelete, path: "/v1/users/admin", auth: auth}); w.Code != http.StatusBadRequest {
		t.Errorf("delete self: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodDelete, path: "/v1/users/nova2", auth: auth}); w.Code != http.StatusNoContent {
		t.Errorf("delete: %d %s", w.Code, w.Body)
	}

	w = f.do(req{method: http.MethodPatch, path: "/v1/settings", auth: auth, body: `{"anon_access":"no-access","allow_keyless":false}`})
	if w.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	s := decodeJSON[ops.Settings](t, w.Body.Bytes())
	if s.AnonAccess != "no-access" || s.AllowKeyless {
		t.Errorf("settings = %+v", s)
	}
	if w := f.do(req{method: http.MethodPatch, path: "/v1/settings", auth: auth, body: `{"anon_access":"lots"}`}); w.Code != http.StatusBadRequest {
		t.Errorf("bad level: %d %s", w.Code, w.Body)
	}
}
