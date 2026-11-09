package web

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/ops"
)

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOYGvLBJnHJYF4P5mO4w0Uc4H0YFqL2bF3zQ5mJ0gV0m"

func TestAPIAccount(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()

	// Anonymous callers have no account to change.
	for _, r := range []req{
		{path: "/v1/me/keys"},
		{path: "/v1/me/tokens"},
		{method: http.MethodPost, path: "/v1/me/tokens", body: `{"name":"x"}`},
		{method: http.MethodPatch, path: "/v1/me", body: `{"username":"x"}`},
	} {
		if w := f.do(r); w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: %d %s", r.method, r.path, w.Code, w.Body)
		}
	}

	// Keys.
	w := f.do(req{method: http.MethodPost, path: "/v1/me/keys", auth: auth, body: `{"key":"` + testKey + `"}`})
	if w.Code != http.StatusCreated {
		t.Fatalf("add key: %d %s", w.Code, w.Body)
	}
	k := decodeJSON[ops.PublicKey](t, w.Body.Bytes())
	if w := f.do(req{method: http.MethodPost, path: "/v1/me/keys", auth: auth, body: `{"key":"` + testKey + `"}`}); w.Code != http.StatusConflict {
		t.Errorf("add again: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodPost, path: "/v1/me/keys", auth: auth, body: `{"key":"nope"}`}); w.Code != http.StatusBadRequest {
		t.Errorf("bad key: %d %s", w.Code, w.Body)
	}
	keys := decodeJSON[[]ops.PublicKey](t, f.do(req{path: "/v1/me/keys", auth: auth}).Body.Bytes())
	if len(keys) == 0 || keys[len(keys)-1].Fingerprint != k.Fingerprint {
		t.Errorf("keys = %+v", keys)
	}
	del := "/v1/me/keys?key=" + url.QueryEscape(k.Fingerprint)
	if w := f.do(req{method: http.MethodDelete, path: del, auth: auth}); w.Code != http.StatusNoContent {
		t.Errorf("remove key: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodDelete, path: del, auth: auth}); w.Code != http.StatusNotFound {
		t.Errorf("remove again: %d %s", w.Code, w.Body)
	}

	// Tokens: the new one works, and is shown only in the answer.
	w = f.do(req{method: http.MethodPost, path: "/v1/me/tokens", auth: auth, body: `{"name":"ci","expires_at":"2999-01-01T00:00:00Z"}`})
	if w.Code != http.StatusCreated || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create token: %d %v %s", w.Code, w.Header(), w.Body)
	}
	tok := decodeJSON[ops.NewToken](t, w.Body.Bytes())
	if tok.Token == "" || tok.ID == 0 || tok.ExpiresAt == nil {
		t.Errorf("token = %+v", tok)
	}
	me := decodeJSON[Account](t, f.do(req{path: "/v1/me", auth: "Token " + tok.Token}).Body.Bytes())
	if me.Username != "owner" {
		t.Errorf("new token is for %+v", me)
	}
	path := "/v1/me/tokens/" + strconv.FormatInt(tok.ID, 10)
	if w := f.do(req{method: http.MethodDelete, path: path, auth: auth}); w.Code != http.StatusNoContent {
		t.Errorf("delete token: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodDelete, path: path, auth: auth}); w.Code != http.StatusNotFound {
		t.Errorf("delete again: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodDelete, path: "/v1/me/tokens/x", auth: auth}); w.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d %s", w.Code, w.Body)
	}

	// Username.
	if w := f.do(req{method: http.MethodPatch, path: "/v1/me", auth: auth, body: `{"username":"-x"}`}); w.Code != http.StatusBadRequest {
		t.Errorf("bad username: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodPatch, path: "/v1/me", auth: auth, body: `{}`}); w.Code != http.StatusBadRequest {
		t.Errorf("empty patch: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodPatch, path: "/v1/me", auth: auth, body: `{"username":"boss"}`}); w.Code != http.StatusNoContent {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	me = decodeJSON[Account](t, f.do(req{path: "/v1/me", auth: auth}).Body.Bytes())
	if me.Username != "boss" {
		t.Errorf("after rename = %+v", me)
	}

	// Writes from another site are refused.
	w = f.do(req{method: http.MethodPost, path: "/v1/me/tokens", auth: auth, body: `{"name":"evil"}`, header: map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}})
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site token: %d %s", w.Code, w.Body)
	}
}
