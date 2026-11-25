package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestUIIssues(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()

	// An empty list offers to create one; anonymous visitors can read but
	// get no way to write.
	has(t, f.do(req{path: "/group/open/-/issues", auth: auth}), "No open issues.", `href="/group/open/-/issues/new"`, `aria-current="page">Issues</a>`)
	hasNot(t, f.do(req{path: "/group/open/-/issues"}), `href="/group/open/-/issues/new"`)
	if w := f.do(req{path: "/group/open/-/issues/new"}); w.Code == http.StatusOK {
		t.Errorf("anonymous new issue form: %d", w.Code)
	}

	// Create from the form; a mistake keeps what was typed.
	w := f.post("/group/open/-/issues/new", auth, url.Values{"title": {""}, "description": {"kept"}})
	has(t, w, "Give the issue a title.", ">kept</textarea>")
	loc := seeOther(t, f.post("/group/open/-/issues/new", auth, url.Values{
		"title": {"Add a board"}, "description": {"With **columns**."}, "status": {"in_progress"},
		"priority": {"urgent"}, "assignee": {"me"}, "new_label": {"web, ui"},
	}), "/group/open/-/issues/1?done=issue-created")
	w = f.do(req{path: loc, auth: auth})
	has(t, w, "Issue created.", "<h1>Add a board</h1>", "<strong>columns</strong>", "In Progress", "Urgent", ">web</span>", ">ui</span>")
	seeOther(t, f.post("/group/open/-/issues/new", auth, url.Values{"title": {"Columns"}, "parent": {"OPE-1"}}), "/group/open/-/issues/2?done=issue-created")

	// The list groups by status and links each issue.
	w = f.do(req{path: "/group/open/-/issues", auth: auth})
	has(t, w, `id="group-in_progress"`, `id="group-todo"`, `href="/group/open/-/issues/1"`, "OPE-2")
	has(t, f.do(req{path: "/group/open/-/issues?q=board", auth: auth}), "Add a board", "Clear filters")
	hasNot(t, f.do(req{path: "/group/open/-/issues?q=board", auth: auth}), ">Columns</a>")
	has(t, f.do(req{path: "/group/open/-/issues?priority=urgent&label=web", auth: auth}), "Add a board")
	has(t, f.do(req{path: "/group/open/-/issues?scope=closed", auth: auth}), "Nothing here.")
	// A bad filter in the URL falls back to the plain list.
	if w := f.do(req{path: "/group/open/-/issues?priority=whenever", auth: auth}); w.Code != http.StatusSeeOther {
		t.Errorf("bad filter: %d", w.Code)
	}

	// The parent lists its sub-issue; the sub-issue links back.
	has(t, f.do(req{path: "/group/open/-/issues/1", auth: auth}), "Sub-issues", `href="/group/open/-/issues/2"`)
	has(t, f.do(req{path: "/group/open/-/issues/2", auth: auth}), `<a href="/group/open/-/issues/1">OPE-1</a>`)

	// Properties: one form, every field; a bad value is explained.
	w = f.post("/group/open/-/issues/2", auth, url.Values{"status": {"done"}, "priority": {"high"}, "assignee": {"nobody-here"}})
	has(t, w, "No user is named &#34;nobody-here&#34;.", " open>")
	seeOther(t, f.post("/group/open/-/issues/2", auth, url.Values{
		"status": {"done"}, "priority": {"high"}, "assignee": {""}, "parent": {"OPE-1"}, "labels": {"web"},
	}), "/group/open/-/issues/2?done=issue-saved")
	w = f.do(req{path: "/group/open/-/issues/2", auth: auth})
	has(t, w, "moved it from Todo to Done", "set the priority to High", "added the label web")

	// Title and description.
	seeOther(t, f.post("/group/open/-/issues/2/edit", auth, url.Values{"title": {"Columns, renamed"}, "description": {"Now with text."}}), "/group/open/-/issues/2?done=issue-saved")
	has(t, f.do(req{path: "/group/open/-/issues/2", auth: auth}), "Columns, renamed", "Now with text.", "renamed it from “Columns”")

	// Comments: add, edit, delete through a confirmation.
	loc = seeOther(t, f.post("/group/open/-/issues/1/comment", auth, url.Values{"body": {"First *thought*."}}), "/group/open/-/issues/1?done=comment-added#comment-1")
	has(t, f.do(req{path: strings.Split(loc, "#")[0], auth: auth}), `id="comment-1"`, "<em>thought</em>", "/group/open/-/issues/1/comments/1/edit")
	w = f.post("/group/open/-/issues/1/comment", auth, url.Values{"body": {"  "}})
	has(t, w, "Write something to comment.")
	seeOther(t, f.post("/group/open/-/issues/1/comments/1/edit", auth, url.Values{"body": {"Second thought."}}), "/group/open/-/issues/1?done=comment-saved#comment-1")
	has(t, f.do(req{path: "/group/open/-/issues/1/comments/1/delete", auth: auth}), "Delete this comment?", "Second thought.")
	seeOther(t, f.post("/group/open/-/issues/1/comments/1/delete", auth, nil), "/group/open/-/issues/1?done=comment-deleted")
	hasNot(t, f.do(req{path: "/group/open/-/issues/1", auth: auth}), `id="comment-1"`)

	// Settings: the key and labels.
	has(t, f.do(req{path: "/group/open/-/issues/settings", auth: auth}), `named <span class="crumbid">OPE-1</span>`, `value="OPE"`, "1 done, 0 canceled")
	seeOther(t, f.post("/group/open/-/issues/settings", auth, url.Values{"op": {"label"}, "name": {"bug"}, "color": {"#e5484d"}}), "/group/open/-/issues/settings?done=label-created")
	has(t, f.do(req{path: "/group/open/-/issues/settings", auth: auth}), `fill="#e5484d"`, " bug</span>")
	// Someone who can only read is refused, not asked to confirm a change
	// they can't make.
	stranger := f.outpost.token(t, "stranger", viewHost, nil)
	for _, form := range []url.Values{
		{"op": {"delete-label"}, "name": {"web"}},
		{"op": {"key"}, "key": {"open"}},
	} {
		for name, who := range map[string]req{"anonymous": {}, "stranger": {identity: stranger}} {
			who.method, who.path, who.body = http.MethodPost, "/group/open/-/issues/settings", form.Encode()
			who.header = map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}
			w := f.do(who)
			if w.Code == http.StatusOK || strings.Contains(w.Body.String(), `name="confirmed"`) {
				t.Errorf("%s by %s: %d, offered a confirmation", form.Get("op"), name, w.Code)
			}
		}
	}
	// Deleting a label asks first and says how many issues have it.
	has(t, f.post("/group/open/-/issues/settings", auth, url.Values{"op": {"delete-label"}, "name": {"web"}}), "Delete this label?", "It comes off 2 issues.")
	seeOther(t, f.post("/group/open/-/issues/settings", auth, url.Values{"op": {"delete-label"}, "name": {"web"}, "confirmed": {"1"}}), "/group/open/-/issues/settings?done=label-deleted")
	// Changing the key shows what every issue becomes, then does it.
	has(t, f.post("/group/open/-/issues/settings", auth, url.Values{"op": {"key"}, "key": {"open"}}), "Rename every issue?", "OPE-1 → OPEN-1", `name="confirmed" value="1"`)
	seeOther(t, f.post("/group/open/-/issues/settings", auth, url.Values{"op": {"key"}, "key": {"OPEN"}, "confirmed": {"1"}}), "/group/open/-/issues/settings?done=key-saved")
	has(t, f.do(req{path: "/group/open/-/issues/1", auth: auth}), "OPEN-1")
	w = f.post("/group/open/-/issues/settings", auth, url.Values{"op": {"key"}, "key": {"9"}})
	has(t, w, "An issue key is 2 to 10 letters")

	// My issues: everything open across repositories, by priority.
	has(t, f.do(req{path: "/-/issues", auth: auth}), "<h1>My issues</h1>", "Add a board", "group/open")
	hasNot(t, f.do(req{path: "/-/issues", auth: auth}), "Columns, renamed")
	has(t, f.do(req{path: "/-/issues?scope=all", auth: auth}), "Columns, renamed")

	// Deleting asks first, and says what goes.
	has(t, f.do(req{path: "/group/open/-/issues/1/delete", auth: auth}), "Delete this issue?", "1 sub-issue lose")
	seeOther(t, f.post("/group/open/-/issues/1/delete", auth, nil), "/group/open/-/issues?done=issue-deleted")
	if w := f.do(req{path: "/group/open/-/issues/1", auth: auth}); w.Code != http.StatusNotFound {
		t.Errorf("deleted issue: %d", w.Code)
	}

	// A private repository's issues stay hidden from those who can't read
	// it, and the forms refuse anonymous posts.
	seeOther(t, f.post("/secret/-/issues/new", auth, url.Values{"title": {"Hidden"}}), "/secret/-/issues/1?done=issue-created")
	if w := f.do(req{path: "/secret/-/issues/1"}); w.Code == http.StatusOK {
		t.Errorf("anonymous read of a private issue: %d", w.Code)
	}
	if w := f.post("/group/open/-/issues/new", "", url.Values{"title": {"x"}}); w.Code == http.StatusSeeOther {
		t.Errorf("anonymous create: %d", w.Code)
	}
}

func TestUIProjectsAndCycles(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()

	has(t, f.do(req{path: "/group/open/-/issues/projects", auth: auth}), "No projects yet.", `aria-current="page">Projects</a>`, "New project", `<details class="addnew" open>`)
	hasNot(t, f.do(req{path: "/group/open/-/issues/projects"}), "New project")

	// A project with a slash in its name, and a mistake first.
	w := f.post("/group/open/-/issues/projects", auth, url.Values{"name": {"Web/v2"}, "status": {"started"}, "start_date": {"2026-12-10"}, "target_date": {"2026-12-01"}})
	has(t, w, "A project&#39;s target date can&#39;t be before its start.", `value="Web/v2"`)
	seeOther(t, f.post("/group/open/-/issues/projects", auth, url.Values{"name": {"Web/v2"}, "status": {"started"}, "target_date": {"2026-12-15"}, "lead": {"me"}}),
		"/group/open/-/issues/projects/1?done=project-created")
	// Its lead is the viewer, so the list doesn't say so; the form for
	// another project is folded away now that there is one.
	w = f.do(req{path: "/group/open/-/issues/projects", auth: auth})
	has(t, w, `href="/group/open/-/issues/projects/1"`, `due <span class="date">2026-12-15</span>`, "0 of 0 done", `<details class="addnew">`)
	hasNot(t, w, "led by owner")

	// Issues go in it from the new-issue form, and its page lists them.
	has(t, f.do(req{path: "/group/open/-/issues/new?project=Web/v2", auth: auth}), `<option value="Web/v2" selected>`)
	seeOther(t, f.post("/group/open/-/issues/new", auth, url.Values{"title": {"Shipped"}, "status": {"done"}, "project": {"Web/v2"}}), "/group/open/-/issues/1")
	seeOther(t, f.post("/group/open/-/issues/new", auth, url.Values{"title": {"Pending"}, "project": {"Web/v2"}}), "/group/open/-/issues/2")
	// A link by name, as an issue's page gives, finds its page.
	if w := f.do(req{path: "/group/open/-/issues/project?name=Web/v2", auth: auth}); w.Code != http.StatusFound || w.Header().Get("Location") != "/group/open/-/issues/projects/1" {
		t.Errorf("project by name: %d %s", w.Code, w.Header().Get("Location"))
	}
	w = f.do(req{path: "/group/open/-/issues/projects/1", auth: auth})
	has(t, w, "<h1>Web/v2</h1>", "1 of 2 done · 50%", "Shipped", "Pending", `id="group-done"`)
	has(t, f.do(req{path: "/group/open/-/issues/1", auth: auth}), `<dt>Project</dt><dd><a href="/group/open/-/issues/project?name=Web%2Fv2">Web/v2</a>`)
	has(t, f.do(req{path: "/group/open/-/issues?project=Web/v2&scope=all", auth: auth}), "Shipped", "Pending")
	hasNot(t, f.do(req{path: "/group/open/-/issues?project=none&scope=all", auth: auth}), "Shipped")

	// Rename it; deleting asks first and keeps its issues.
	// Renaming keeps its address.
	seeOther(t, f.post("/group/open/-/issues/projects/1", auth, url.Values{"name": {"Web v2"}, "status": {"completed"}}), "/group/open/-/issues/projects/1?done=project-saved")
	// A form without the lead field, as with one user, leaves the lead be.
	has(t, f.do(req{path: "/group/open/-/issues/projects/1", auth: auth}), "<dt>Lead</dt><dd>owner</dd>")
	has(t, f.do(req{path: "/group/open/-/issues/projects/1/delete", auth: auth}), "Delete this project?", "Its 2 issues stay, in no project.")
	seeOther(t, f.post("/group/open/-/issues/projects/1/delete", auth, nil), "/group/open/-/issues/projects?done=project-deleted")
	hasNot(t, f.do(req{path: "/group/open/-/issues/1", auth: auth}), "<dt>Project</dt>")

	// Cycles: the next one follows the last; overlaps are refused.
	seeOther(t, f.post("/group/open/-/issues/cycles", auth, url.Values{"start_date": {"2027-01-04"}}), "/group/open/-/issues/cycles/1?done=cycle-created")
	seeOther(t, f.post("/group/open/-/issues/cycles", auth, url.Values{"name": {"Polish"}}), "/group/open/-/issues/cycles/2?done=cycle-created")
	w = f.post("/group/open/-/issues/cycles", auth, url.Values{"start_date": {"2027-01-10"}, "end_date": {"2027-01-12"}})
	has(t, w, "Those dates overlap cycle 1")
	has(t, f.do(req{path: "/group/open/-/issues/cycles", auth: auth}), "Cycle 1", `<span class="date">2027-01-04</span> to <span class="date">2027-01-17</span>`, "Cycle 2: Polish", `<span class="date">2027-01-18</span>`)

	// Put an issue in a cycle from its property form.
	seeOther(t, f.post("/group/open/-/issues/2", auth, url.Values{"status": {"todo"}, "priority": {"none"}, "cycle": {"2"}}), "/group/open/-/issues/2?done=issue-saved")
	has(t, f.do(req{path: "/group/open/-/issues/2", auth: auth}), `<dt>Cycle</dt><dd><a href="/group/open/-/issues/cycles/2">Cycle 2</a>`, "moved it into cycle 2")
	has(t, f.do(req{path: "/group/open/-/issues/cycles/2", auth: auth}), "Cycle 2: Polish", "Pending", "0 of 1 done")
	seeOther(t, f.post("/group/open/-/issues/cycles/2", auth, url.Values{"name": {"Polish"}, "start_date": {"2027-01-18"}, "end_date": {"2027-02-07"}}), "/group/open/-/issues/cycles/2?done=cycle-saved")
	seeOther(t, f.post("/group/open/-/issues/cycles/2/delete", auth, nil), "/group/open/-/issues/cycles?done=cycle-deleted")
	if w := f.do(req{path: "/group/open/-/issues/cycles/2", auth: auth}); w.Code != http.StatusNotFound {
		t.Errorf("deleted cycle: %d", w.Code)
	}
}

func TestUIBoardAndQuick(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()
	for _, title := range []string{"One", "Two", "Three"} {
		seeOther(t, f.post("/group/open/-/issues/new", auth, url.Values{"title": {title}}), "/group/open/-/issues/")
	}

	// The board: every column but canceled, cards in manual order, with
	// menus for those who can write and the script on the page.
	w := f.do(req{path: "/group/open/-/issues/board", auth: auth})
	has(t, w, `data-status="backlog"`, `data-status="todo"`, `data-status="in_progress"`, `data-status="in_review"`, `data-status="done"`,
		`data-quick="/group/open/-/issues/1/quick"`, `class="qmenu"`, `/-/static/issues.js?v=`, `id="shortcuts" hidden`)
	hasNot(t, w, `data-status="canceled"`)
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") || strings.Contains(csp, "unsafe") {
		t.Errorf("CSP: %s", csp)
	}
	hasNot(t, f.do(req{path: "/group/open/-/issues/board"}), `class="qmenu"`)
	w = f.do(req{path: "/-/static/issues.js"})
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
		t.Errorf("script: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	// Pages that don't need it don't load it.
	hasNot(t, f.do(req{path: "/group/open", auth: auth}), "issues.js")

	// Without script, a menu's form changes only what it sends and goes
	// back where it came from.
	seeOther(t, f.post("/group/open/-/issues/2/quick", auth, url.Values{"status": {"in_progress"}, "back": {"/group/open/-/issues/board?assignee=me"}}),
		"/group/open/-/issues/board?assignee=me&done=issue-saved")
	seeOther(t, f.post("/group/open/-/issues/2/quick", auth, url.Values{"priority": {"high"}, "back": {"//evil.example/"}}), "/group/open/-/issues/2?done=issue-saved")
	seeOther(t, f.post("/group/open/-/issues/2/quick", auth, url.Values{"priority": {"high"}, "back": {"https://evil.example/"}}), "/group/open/-/issues/2?done=issue-saved")
	has(t, f.do(req{path: "/group/open/-/issues/2", auth: auth}), "In Progress", "High")

	// From the script: 204, or the reason in JSON.
	scripted := func(form url.Values) *httptest.ResponseRecorder {
		return f.do(req{
			method: http.MethodPost, path: "/group/open/-/issues/3/quick", auth: auth, body: form.Encode(),
			header: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "X-Requested-With": "soft-serve"},
		})
	}
	if w := scripted(url.Values{"after": {"top"}}); w.Code != http.StatusNoContent {
		t.Errorf("move to top: %d %s", w.Code, w.Body)
	}
	if w := scripted(url.Values{"status": {"shipped"}}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error":"Unknown status`) {
		t.Errorf("bad status: %d %s", w.Code, w.Body)
	}
	w = f.do(req{path: "/group/open/-/issues/board", auth: auth})
	body := w.Body.String()
	if i, j := strings.Index(body, `data-issue="3"`), strings.Index(body, `data-issue="1"`); i < 0 || j < 0 || i > j {
		t.Errorf("OPE-3 isn't above OPE-1 after moving it to the top")
	}

	// Readers can't use it, with or without script.
	if w := f.post("/group/open/-/issues/1/quick", "", url.Values{"status": {"done"}}); w.Code == http.StatusSeeOther {
		t.Errorf("anonymous quick change: %d", w.Code)
	}
}

// One way to change status and priority, on every view.
func TestUIOneEditingModel(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()
	seeOther(t, f.post("/group/open/-/issues/projects", auth, url.Values{"name": {"Board"}, "status": {"started"}}), "/group/open/-/issues/projects/1")
	seeOther(t, f.post("/group/open/-/issues/cycles", auth, url.Values{}), "/group/open/-/issues/cycles/1")
	seeOther(t, f.post("/group/open/-/issues/new", auth, url.Values{"title": {"Columns"}, "project": {"Board"}, "cycle": {"1"}, "assignee": {"me"}}), "/group/open/-/issues/1")

	for _, path := range []string{"/group/open/-/issues/projects/1", "/group/open/-/issues/cycles/1", "/group/open/-/issues"} {
		w := f.do(req{path: path, auth: auth})
		has(t, w, `data-issue="1"`, `action="/group/open/-/issues/1/quick"`, "/-/static/issues.js")
		// Assigned to the viewer, so not named on the row.
		hasNot(t, w, `class="iassignee"`)
	}
	// On the issue's page, the side panel's status and priority are the
	// same menus, showing their names; the form holds the rest.
	w := f.do(req{path: "/group/open/-/issues/1", auth: auth})
	has(t, w, `class="qmenu wide"`, `<span>Todo</span>`, "Labels, project and more")
	hasNot(t, w, `<select name="status">`)
	// Saving that form leaves status and priority alone.
	seeOther(t, f.post("/group/open/-/issues/1/quick", auth, url.Values{"status": {"in_progress"}}), "/group/open/-/issues/1")
	seeOther(t, f.post("/group/open/-/issues/1", auth, url.Values{"assignee": {""}, "labels": {}, "new_label": {"web"}}), "/group/open/-/issues/1?done=issue-saved")
	has(t, f.do(req{path: "/group/open/-/issues/1", auth: auth}), "<span>In Progress</span>", ">web</span>")
	// Menus number their options for the keyboard.
	has(t, w, `<kbd class="qkey" aria-hidden="true">1</kbd>`)
}

func TestShowWho(t *testing.T) {
	for _, c := range []struct {
		who, viewer string
		solo, want  bool
	}{
		{"", "me", false, false},
		{"alice", "alice", false, false},
		{"alice", "bob", true, false},
		{"alice", "bob", false, true},
	} {
		if got := showWho(c.who, c.viewer, c.solo); got != c.want {
			t.Errorf("showWho(%q, %q, %v) = %v", c.who, c.viewer, c.solo, got)
		}
	}
}
