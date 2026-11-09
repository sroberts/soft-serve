package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// post submits a form as a browser on the same site would.
func (f *viewFixture) post(path, auth string, form url.Values) *httptest.ResponseRecorder {
	return f.do(req{
		method: http.MethodPost,
		path:   path,
		auth:   auth,
		body:   form.Encode(),
		header: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"},
	})
}

// seeOther checks a change redirected, and returns the page it went to.
func seeOther(t *testing.T, w *httptest.ResponseRecorder, wantPrefix string) string {
	t.Helper()
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303:\n%s", w.Code, w.Body)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, wantPrefix) {
		t.Fatalf("redirected to %q, want %q...", loc, wantPrefix)
	}
	return loc
}

func has(t *testing.T, w *httptest.ResponseRecorder, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("page has no %q (status %d)", want, w.Code)
		}
	}
}

func hasNot(t *testing.T, w *httptest.ResponseRecorder, nots ...string) {
	t.Helper()
	for _, not := range nots {
		if strings.Contains(w.Body.String(), not) {
			t.Errorf("page has %q but shouldn't (status %d)", not, w.Code)
		}
	}
}

func TestUINewRepository(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()

	// Signed in, the index offers it; anonymous callers get neither the
	// link nor the form.
	has(t, f.do(req{path: "/", auth: auth}), `href="/-/new"`)
	hasNot(t, f.do(req{path: "/"}), `href="/-/new"`)
	w := f.do(req{path: "/-/new"})
	has(t, w, "Sign in to create a repository.")
	hasNot(t, w, `<form class="manage"`)
	has(t, f.do(req{path: "/-/new", auth: auth}), `name="source" value="mirror"`, `name="auto_sync" checked`)

	loc := seeOther(t, f.post("/-/new", auth, url.Values{"name": {"made"}, "description": {"from a form"}, "private": {"on"}}), "/made?done=created")
	w = f.do(req{path: loc, auth: auth})
	has(t, w, `<p class="notice" role="status">Repository created.</p>`, "from a form", `<span class="badge">private</span>`)

	// A failed create shows the form again, with what was typed.
	w = f.post("/-/new", auth, url.Values{"name": {"bad name!"}, "description": {"kept"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad name: %d", w.Code)
	}
	has(t, w, `class="formerror" role="alert"`, `value="kept"`)
	w = f.post("/-/new", auth, url.Values{"name": {"made"}})
	if w.Code != http.StatusConflict {
		t.Errorf("existing name: %d", w.Code)
	}
	has(t, w, "Repository already exists.")
	w = f.post("/-/new", auth, url.Values{"name": {"forked"}, "source": {"fork"}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("fork without remote: %d", w.Code)
	}

	// Anonymous callers are asked to sign in.
	w = f.post("/-/new", "", url.Values{"name": {"anon"}})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous create: %d", w.Code)
	}
	has(t, w, "Sign in to do that.")
}

func TestUIChangesRefusedCrossSite(t *testing.T) {
	f := newViewFixture(t)
	w := f.do(req{
		method: http.MethodPost,
		path:   "/-/new",
		auth:   f.owner(),
		body:   "name=csrf",
		header: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "cross-site"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site post: %d", w.Code)
	}
	if _, err := f.be.Repository(f.ctx, "csrf"); err == nil {
		t.Error("cross-site post created a repository")
	}

	// A POST to a page that only reads is no route at all.
	if w := f.post("/group/open/-/tree", f.owner(), url.Values{}); w.Code != http.StatusNotFound {
		t.Errorf("post to a read page: %d", w.Code)
	}
	// Only known notices are shown.
	hasNot(t, f.do(req{path: "/group/open?done=%3Cb%3Ehi", auth: f.owner()}), `class="notice"`)
}

func TestUISettings(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()
	stranger := f.outpost.token(t, "stranger", viewHost, nil)

	w := f.do(req{path: "/group/open/-/settings", auth: auth})
	has(t, w, `action="/group/open/-/settings"`, `value="An open one"`, `<option selected>main</option>`, `href="/group/open/-/delete"`)
	hasNot(t, w, `name="auto_sync"`) // not a mirror
	has(t, f.do(req{path: "/group/open", auth: auth}), `href="/group/open/-/settings"`)

	// A reader sees neither the tab nor the page.
	hasNot(t, f.do(req{path: "/group/open", identity: stranger}), `/-/settings"`)
	if w := f.do(req{path: "/group/open/-/settings", identity: stranger}); w.Code != http.StatusForbidden {
		t.Errorf("stranger settings: %d", w.Code)
	}
	if w := f.post("/group/open/-/settings", "", url.Values{"description": {"x"}}); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous save: %d", w.Code)
	}

	loc := seeOther(t, f.post("/group/open/-/settings", auth, url.Values{
		"description": {"Changed"}, "project_name": {""}, "hidden": {"on"}, "default_branch": {"main"},
	}), "/group/open/-/settings?done=saved")
	has(t, f.do(req{path: loc, auth: auth}), "Settings saved.", `value="Changed"`, `name="hidden" checked`)
	r, err := f.be.Repository(f.ctx, "group/open")
	if err != nil || r.Description() != "Changed" || !r.IsHidden() {
		t.Fatalf("after save: %v %q hidden=%v", err, r.Description(), r.IsHidden())
	}

	// Syncing something that isn't a mirror says so on the settings page.
	w = f.post("/group/open/-/sync", auth, url.Values{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("sync a non-mirror: %d", w.Code)
	}
	has(t, w, "Repository is not a mirror.", `action="/group/open/-/settings"`)

	// Rename, then delete after confirming.
	loc = seeOther(t, f.post("/group/open/-/rename", auth, url.Values{"name": {"group/moved"}}), "/group/moved/-/settings?done=renamed")
	has(t, f.do(req{path: loc, auth: auth}), "Repository renamed.")
	w = f.do(req{path: "/group/moved/-/delete", auth: auth})
	has(t, w, "Delete this repository?", `action="/group/moved/-/delete"`)
	if _, err := f.be.Repository(f.ctx, "group/moved"); err != nil {
		t.Fatal("the confirmation page deleted it")
	}
	seeOther(t, f.post("/group/moved/-/delete", auth, url.Values{}), "/?done=deleted")
	if _, err := f.be.Repository(f.ctx, "group/moved"); err == nil {
		t.Error("repository still there after delete")
	}
}

func TestUIDeleteRefs(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()
	dir := filepath.Join(config.FromContext(f.ctx).DataPath, "repos", "group/open.git")
	for _, args := range [][]string{{"branch", "topic", "main"}, {"tag", "v1", "main"}} {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"--git-dir", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	w := f.do(req{path: "/group/open/-/refs", auth: auth})
	has(t, w, `href="/group/open/-/refs/delete?branch=topic"`, `href="/group/open/-/refs/delete?tag=v1"`)
	hasNot(t, w, `refs/delete?branch=main`)
	hasNot(t, f.do(req{path: "/group/open/-/refs"}), "refs/delete")

	w = f.do(req{path: "/group/open/-/refs/delete?branch=topic", auth: auth})
	has(t, w, "Delete this branch?", `name="branch" value="topic"`)
	seeOther(t, f.post("/group/open/-/refs/delete", auth, url.Values{"branch": {"topic"}}), "/group/open/-/refs?done=branch-deleted")
	seeOther(t, f.post("/group/open/-/refs/delete", auth, url.Values{"tag": {"v1"}}), "/group/open/-/refs?done=tag-deleted")
	w = f.do(req{path: "/group/open/-/refs", auth: auth})
	hasNot(t, w, ">topic<", ">v1<")

	if w := f.post("/group/open/-/refs/delete", auth, url.Values{"branch": {"main"}}); w.Code != http.StatusBadRequest {
		t.Errorf("delete default branch: %d", w.Code)
	}
	if w := f.post("/group/open/-/refs/delete", "", url.Values{"branch": {"main"}}); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous delete: %d", w.Code)
	}
}

func TestUICollaborators(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()
	stranger := f.outpost.token(t, "stranger", viewHost, nil)

	has(t, f.do(req{path: "/secret/-/access", auth: auth}), `action="/secret/-/access/add"`, "No collaborators yet.")
	seeOther(t, f.post("/secret/-/access/add", auth, url.Values{"username": {"stranger"}, "access": {"read-only"}}), "/secret/-/access?done=collab-added")
	w := f.do(req{path: "/secret/-/access?done=collab-added", auth: auth})
	has(t, w, "Collaborator added.", `<span class="refname">stranger</span><span class="badge">read-only</span>`, `aria-label="Remove stranger"`)

	// Adding them again fails on the form, which keeps what was typed.
	w = f.post("/secret/-/access/add", auth, url.Values{"username": {"stranger"}, "access": {"admin-access"}})
	if w.Code != http.StatusConflict {
		t.Errorf("add twice: %d", w.Code)
	}
	has(t, w, "Collaborator already exists.", `value="stranger"`, `<option selected>admin-access</option>`)

	// Anonymous callers and readers can't manage collaborators.
	if w := f.post("/secret/-/access/add", "", url.Values{"username": {"x"}}); w.Code != http.StatusNotFound {
		t.Errorf("anonymous add on a private repository: %d", w.Code)
	}
	wr := f.do(req{
		method: http.MethodPost, path: "/secret/-/access/remove", identity: stranger, body: "username=stranger",
		header: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
	})
	if wr.Code != http.StatusForbidden {
		t.Errorf("reader remove: %d", wr.Code)
	}

	seeOther(t, f.post("/secret/-/access/remove", auth, url.Values{"username": {"stranger"}}), "/secret/-/access?done=collab-removed")
	if _, ok, _ := f.be.IsCollaborator(f.ctx, "secret", "stranger"); ok {
		t.Error("still a collaborator")
	}

	// Read-write collaborators get the page too, as over SSH.
	owner, _ := f.be.User(f.ctx, "owner")
	if err := f.be.AddCollaborator(context.WithValue(f.ctx, proto.ContextKeyUser, owner), "secret", "stranger", access.ReadWriteAccess); err != nil {
		t.Fatal(err)
	}
	w = f.do(req{path: "/secret/-/access", identity: stranger})
	if w.Code != http.StatusOK {
		t.Errorf("writer access page: %d", w.Code)
	}
	hasNot(t, f.do(req{path: "/secret", identity: stranger}), `/-/webhooks"`)
}

func TestUIWebhooks(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()
	stranger := f.outpost.token(t, "stranger", viewHost, nil)

	has(t, f.do(req{path: "/group/open", auth: auth}), `href="/group/open/-/webhooks"`)
	w := f.do(req{path: "/group/open/-/webhooks", auth: auth})
	has(t, w, "No webhooks yet.", `value="push" checked`)
	if w := f.do(req{path: "/group/open/-/webhooks", identity: stranger}); w.Code != http.StatusForbidden {
		t.Errorf("stranger webhooks: %d", w.Code)
	}

	// A private address is refused, on the form.
	w = f.post("/group/open/-/webhooks", auth, url.Values{"url": {"http://127.0.0.1/hook"}, "events": {"push"}, "content_type": {"json"}, "active": {"on"}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("private url: %d", w.Code)
	}
	has(t, w, "Invalid webhook URL", `value="http://127.0.0.1/hook"`)
	w = f.post("/group/open/-/webhooks", auth, url.Values{"url": {"https://example.com/hook"}, "content_type": {"json"}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("no events: %d", w.Code)
	}

	seeOther(t, f.post("/group/open/-/webhooks", auth, url.Values{
		"url": {"https://example.com/hook"}, "events": {"push", "repository"}, "content_type": {"form"}, "secret": {"shh"}, "active": {"on"},
	}), "/group/open/-/webhooks?done=hook-created")
	w = f.do(req{path: "/group/open/-/webhooks", auth: auth})
	has(t, w, `href="/group/open/-/webhooks/1"`, "push, repository · form")
	hasNot(t, w, "shh")

	w = f.do(req{path: "/group/open/-/webhooks/1", auth: auth})
	has(t, w, `value="https://example.com/hook"`, `value="repository" checked`, `<option value="form" selected>`, "Nothing sent yet.", `href="/group/open/-/webhooks/1/delete"`)
	hasNot(t, w, "shh")

	// Saving without the active box turns it off; the secret is kept.
	seeOther(t, f.post("/group/open/-/webhooks/1", auth, url.Values{"url": {"https://example.com/hook"}, "events": {"push"}, "content_type": {"json"}}), "/group/open/-/webhooks/1?done=hook-saved")
	has(t, f.do(req{path: "/group/open/-/webhooks", auth: auth}), `<span class="badge">inactive</span>`)
	r, _ := f.be.Repository(f.ctx, "group/open")
	hook, err := f.be.Webhook(f.ctx, r, 1)
	if err != nil || hook.Active || hook.Secret != "shh" || len(hook.Events) != 1 {
		t.Errorf("after save: %+v %v", hook, err)
	}

	if w := f.do(req{path: "/group/open/-/webhooks/1/deliveries/not-a-uuid", auth: auth}); w.Code != http.StatusNotFound {
		t.Errorf("bad delivery: %d", w.Code)
	}
	if w := f.do(req{path: "/group/open/-/webhooks/99", auth: auth}); w.Code != http.StatusNotFound {
		t.Errorf("missing webhook: %d", w.Code)
	}

	has(t, f.do(req{path: "/group/open/-/webhooks/1/delete", auth: auth}), "Remove this webhook?")
	seeOther(t, f.post("/group/open/-/webhooks/1/delete", auth, url.Values{}), "/group/open/-/webhooks?done=hook-deleted")
	has(t, f.do(req{path: "/group/open/-/webhooks", auth: auth}), "No webhooks yet.")
}
