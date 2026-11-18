package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/ssrf"
	"github.com/charmbracelet/soft-serve/pkg/utils"
	"github.com/charmbracelet/soft-serve/pkg/webhook"
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
	has(t, w, `class="formerror" role="alert"`, `value="kept"`, "A repository name can use only letters, numbers, -, _, . and /.")
	w = f.post("/-/new", auth, url.Values{"name": {"made"}})
	if w.Code != http.StatusConflict {
		t.Errorf("existing name: %d", w.Code)
	}
	has(t, w, "There&#39;s already a repository with that name. Choose another.")
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
	// Nor can a reader delete it, even typing the right name, or see the
	// question's branch and tag counts.
	w = f.do(req{path: "/group/open/-/delete", identity: stranger})
	wantStatus(t, w, http.StatusForbidden, "stranger delete page")
	hasNot(t, w, "every commit on them")
	w = f.do(req{
		method: http.MethodPost, path: "/group/open/-/delete", identity: stranger, body: "confirm=group%2Fopen",
		header: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"},
	})
	wantStatus(t, w, http.StatusForbidden, "stranger delete")
	if _, err := f.be.Repository(f.ctx, "group/open"); err != nil {
		t.Fatal("a reader deleted the repository")
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
	has(t, w, "Type <code>group/moved</code> to confirm</span>", "this server holds the only copy")
	has(t, w, "every commit on them")
	// The name is checked on the server: missing, the old name, the last
	// part alone or a different case delete nothing.
	for _, typed := range []string{"", "group/open", "moved", "Group/Moved"} {
		w = f.post("/group/moved/-/delete", auth, url.Values{"confirm": {typed}})
		wantStatus(t, w, http.StatusBadRequest, "delete typing "+typed)
		has(t, w, esc("That isn't the name."), `aria-invalid="true"`)
		if _, err := f.be.Repository(f.ctx, "group/moved"); err != nil {
			t.Fatalf("deleted typing %q", typed)
		}
	}
	// A wrong name stays in the field to be corrected, and the browser is
	// told what matches.
	w = f.post("/group/moved/-/delete", auth, url.Values{"confirm": {"moved"}})
	has(t, w, `value="moved"`, `pattern="\s*group/moved\s*"`)
	// Space around the name, as a paste brings, is fine.
	seeOther(t, f.post("/group/moved/-/delete", auth, url.Values{"confirm": {" group/moved\n"}}), "/?done=deleted")
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
	has(t, w, "Delete this branch?", `name="branch" value="topic"`, "so nothing else is lost")

	// A branch with a commit of its own says how many go with it.
	git := func(args ...string) string {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"--git-dir", dir}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	sha := git("commit-tree", "main^{tree}", "-p", "main", "-m", "solo")
	git("update-ref", "refs/heads/solo", sha)
	has(t, f.do(req{path: "/group/open/-/refs/delete?branch=solo", auth: auth}), "1 commit on it, on no other branch or tag, will no longer be reachable.")
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
	has(t, w, "Collaborator added.", `<span class="refname">stranger</span><span class="badge">Read-only</span>`, `aria-label="Remove stranger"`)

	// Adding them again fails on the form, which keeps what was typed.
	w = f.post("/secret/-/access/add", auth, url.Values{"username": {"stranger"}, "access": {"admin-access"}})
	if w.Code != http.StatusConflict {
		t.Errorf("add twice: %d", w.Code)
	}
	has(t, w, "They&#39;re already a collaborator.", `value="stranger"`, `<option value="admin-access" selected>Admin</option>`)

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

	// Webhooks are under Manage: one tab, then its own row of pages.
	has(t, f.do(req{path: "/group/open", auth: auth}), `href="/group/open/-/settings">Manage</a>`)
	has(t, f.do(req{path: "/group/open/-/settings", auth: auth}), `<nav class="subnav" aria-label="Manage">`, `href="/group/open/-/webhooks"`)
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
	has(t, w, "That address is on a private or internal network, which this server won&#39;t connect to.", `value="http://127.0.0.1/hook"`)
	w = f.post("/group/open/-/webhooks", auth, url.Values{"url": {"https://example.com/hook"}, "content_type": {"json"}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("no events: %d", w.Code)
	}

	seeOther(t, f.post("/group/open/-/webhooks", auth, url.Values{
		"url": {"https://example.com/hook"}, "events": {"push", "repository"}, "content_type": {"form"}, "secret": {"shh"}, "active": {"on"},
	}), "/group/open/-/webhooks?done=hook-created")
	w = f.do(req{path: "/group/open/-/webhooks", auth: auth})
	has(t, w, `href="/group/open/-/webhooks/1"`, "Commits are pushed; The repository is renamed or deleted · form")
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

	has(t, f.do(req{path: "/group/open/-/webhooks/1/delete", auth: auth}), "Delete this webhook?")
	seeOther(t, f.post("/group/open/-/webhooks/1/delete", auth, url.Values{}), "/group/open/-/webhooks?done=hook-deleted")
	has(t, f.do(req{path: "/group/open/-/webhooks", auth: auth}), "No webhooks yet.")
}

// The service's terse messages are put in plain words on pages, and every
// phrase looked for is one the service still says.
func TestPlainWords(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{proto.ErrCollaboratorExist, "They're already a collaborator. To change their access, remove them and add them again."},
		{fmt.Errorf("%w: %q", ssrf.ErrSSHHostNotAllowed, "example.com:22"), "The server's config doesn't allow that SSH host (client_allowed_hosts)."},
		{ops.Invalid("invalid webhook URL: %v", fmt.Errorf("%w: cannot resolve hostname", ssrf.ErrInvalidURL)), "That address's host name couldn't be found."},
		{ops.Invalid("%v", utils.ValidateRepo("a b")), "A repository name can use only letters, numbers, -, _, . and /."},
		{ops.Invalid("%v", utils.ValidateUsername("1a")), "A username has to start with a letter."},
		{ops.Invalid("give the token a name"), "Give the token a name."},
	} {
		if got := plainMessage(c.err); got != c.want {
			t.Errorf("plainMessage(%q) = %q, want %q", c.err, got, c.want)
		}
	}
	for _, err := range []error{
		utils.ValidateRepo(""), utils.ValidateRepo("a b"), utils.ValidateRepo("a.git/b"),
		utils.ValidateUsername(""), utils.ValidateUsername("1"), utils.ValidateUsername("a b"),
	} {
		if plainMessage(err) == sentence(err.Error()) {
			t.Errorf("%q has no plain words", err)
		}
	}
}

// Every webhook event has words of its own on the form.
func TestEventLabels(t *testing.T) {
	for _, e := range webhook.Events() {
		if eventLabel(e.String()) == e.String() {
			t.Errorf("event %q has no label", e)
		}
	}
}
