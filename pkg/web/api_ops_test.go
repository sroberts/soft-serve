package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/ops"
)

func (f *viewFixture) owner() string { return "Token " + f.ownerToken }

func decodeJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return v
}

func TestAPIManageRepo(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()

	w := f.do(req{method: http.MethodPost, path: "/v1/repos", auth: auth, body: `{"name":"made","description":"by api","private":true}`})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	created := decodeJSON[ops.Repo](t, w.Body.Bytes())
	if created.Name != "made" || created.Owner != "owner" || !created.Private || created.Description != "by api" {
		t.Errorf("created = %+v", created)
	}

	w = f.do(req{method: http.MethodPost, path: "/v1/repos", auth: auth, body: `{"name":"made"}`})
	if w.Code != http.StatusConflict {
		t.Errorf("create again: %d %s", w.Code, w.Body)
	}
	w = f.do(req{method: http.MethodPost, path: "/v1/repos", auth: auth, body: `{"name":"bad name!"}`})
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad name: %d %s", w.Code, w.Body)
	}
	w = f.do(req{method: http.MethodPost, path: "/v1/repos", auth: auth, body: `{"name":"x","privat":true}`})
	if w.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d %s", w.Code, w.Body)
	}

	w = f.do(req{method: http.MethodPatch, path: "/v1/repos/made", auth: auth, body: `{"description":"changed","hidden":true}`})
	if w.Code != http.StatusNoContent {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	w = f.do(req{path: "/v1/repos/made/-/info", auth: auth})
	info := decodeJSON[ops.Repo](t, w.Body.Bytes())
	if info.Description != "changed" || !info.Hidden || info.Access != "admin-access" {
		t.Errorf("info after patch = %+v", info)
	}

	// Hidden repositories are listed only when asked for.
	if names := repoNames(t, f.do(req{path: "/v1/repos", auth: auth})); contains(names, "made") {
		t.Errorf("hidden repo listed: %v", names)
	}
	if names := repoNames(t, f.do(req{path: "/v1/repos?hidden=true", auth: auth})); !contains(names, "made") {
		t.Errorf("hidden=true missing repo: %v", names)
	}

	w = f.do(req{method: http.MethodPost, path: "/v1/repos/made/-/rename", auth: auth, body: `{"name":"renamed"}`})
	if w.Code != http.StatusNoContent {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	w = f.do(req{method: http.MethodPost, path: "/v1/repos/renamed/-/sync", auth: auth})
	if w.Code != http.StatusBadRequest {
		t.Errorf("sync a non-mirror: %d %s", w.Code, w.Body)
	}

	w = f.do(req{method: http.MethodDelete, path: "/v1/repos/renamed", auth: auth})
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	w = f.do(req{path: "/v1/repos/renamed/-/info", auth: auth})
	if w.Code != http.StatusNotFound {
		t.Errorf("info after delete: %d", w.Code)
	}
}

func contains(xs []string, x string) bool { return slices.Contains(xs, x) }

func TestAPIManageAccess(t *testing.T) {
	f := newViewFixture(t)

	// Anonymous callers are asked to sign in; signed-in strangers are told
	// the private repository is not there.
	w := f.do(req{method: http.MethodPatch, path: "/v1/repos/secret", body: `{"description":"x"}`})
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("anonymous patch: %d %v", w.Code, w.Header())
	}
	w = f.do(req{method: http.MethodPost, path: "/v1/repos", body: `{"name":"anon"}`})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous create: %d %s", w.Code, w.Body)
	}
	stranger := f.outpost.token(t, "stranger", viewHost, nil)
	w = f.do(req{method: http.MethodDelete, path: "/v1/repos/secret", identity: stranger})
	if w.Code != http.StatusNotFound {
		t.Errorf("stranger delete: %d %s", w.Code, w.Body)
	}
	w = f.do(req{method: http.MethodPatch, path: "/v1/repos/group/open", identity: stranger, body: `{"description":"x"}`})
	if w.Code != http.StatusForbidden {
		t.Errorf("stranger patch public: %d %s", w.Code, w.Body)
	}

	// A browser on another site cannot make changes with the caller's
	// credentials, while the same request from the page itself goes on.
	w = f.do(req{method: http.MethodPatch, path: "/v1/repos/group/open", auth: f.owner(), body: `{"description":"x"}`, header: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site patch: %d %s", w.Code, w.Body)
	}
	w = f.do(req{method: http.MethodPatch, path: "/v1/repos/group/open", auth: f.owner(), body: `{"description":"x"}`, header: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	if w.Code != http.StatusNoContent {
		t.Errorf("same-origin patch: %d %s", w.Code, w.Body)
	}
	// Reads are never refused for their origin.
	w = f.do(req{path: "/v1/repos/group/open/-/info", auth: f.owner(), header: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	if w.Code != http.StatusOK {
		t.Errorf("cross-site read: %d", w.Code)
	}
}

func TestAPICollaboratorsAndWebhooks(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()

	w := f.do(req{method: http.MethodPost, path: "/v1/repos/secret/-/collaborators", auth: auth, body: `{"username":"stranger","access":"read-only"}`})
	if w.Code != http.StatusCreated {
		t.Fatalf("add collaborator: %d %s", w.Code, w.Body)
	}
	w = f.do(req{path: "/v1/repos/secret/-/collaborators", auth: auth})
	cs := decodeJSON[[]ops.Collaborator](t, w.Body.Bytes())
	if len(cs) != 1 || cs[0].Username != "stranger" || cs[0].Access != "read-only" {
		t.Errorf("collaborators = %+v", cs)
	}
	// Now a reader, the stranger can see the repository but not change it.
	stranger := f.outpost.token(t, "stranger", viewHost, nil)
	w = f.do(req{method: http.MethodPatch, path: "/v1/repos/secret", identity: stranger, body: `{"description":"x"}`})
	if w.Code != http.StatusForbidden {
		t.Errorf("reader patch: %d %s", w.Code, w.Body)
	}
	w = f.do(req{method: http.MethodDelete, path: "/v1/repos/secret/-/collaborators/stranger", auth: auth})
	if w.Code != http.StatusNoContent {
		t.Fatalf("remove collaborator: %d %s", w.Code, w.Body)
	}

	w = f.do(req{method: http.MethodPost, path: "/v1/repos/secret/-/webhooks", auth: auth, body: `{"url":"https://example.com/hook","events":["push"],"secret":"shh"}`})
	if w.Code != http.StatusCreated {
		t.Fatalf("create webhook: %d %s", w.Code, w.Body)
	}
	hook := decodeJSON[ops.Webhook](t, w.Body.Bytes())
	if strings.Contains(w.Body.String(), "shh") {
		t.Error("webhook secret returned")
	}
	w = f.do(req{method: http.MethodPost, path: "/v1/repos/secret/-/webhooks", auth: auth, body: `{"url":"http://127.0.0.1/hook","events":["push"]}`})
	if w.Code != http.StatusBadRequest {
		t.Errorf("private webhook url: %d %s", w.Code, w.Body)
	}

	w = f.do(req{method: http.MethodPatch, path: "/v1/repos/secret/-/webhooks/" + itoa(hook.ID), auth: auth, body: `{"active":false}`})
	if w.Code != http.StatusNoContent {
		t.Fatalf("update webhook: %d %s", w.Code, w.Body)
	}
	w = f.do(req{path: "/v1/repos/secret/-/webhooks", auth: auth})
	hooks := decodeJSON[[]ops.Webhook](t, w.Body.Bytes())
	if len(hooks) != 1 || hooks[0].Active {
		t.Errorf("webhooks = %+v", hooks)
	}
	w = f.do(req{path: "/v1/repos/secret/-/webhooks/" + itoa(hook.ID) + "/deliveries", auth: auth})
	if w.Code != http.StatusOK {
		t.Errorf("deliveries: %d %s", w.Code, w.Body)
	}
	w = f.do(req{path: "/v1/repos/secret/-/webhooks/" + itoa(hook.ID) + "/deliveries/nope", auth: auth})
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad delivery id: %d %s", w.Code, w.Body)
	}
	w = f.do(req{method: http.MethodDelete, path: "/v1/repos/secret/-/webhooks/" + itoa(hook.ID), auth: auth})
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete webhook: %d %s", w.Code, w.Body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
