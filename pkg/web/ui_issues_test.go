package web

import (
	"net/http"
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
	has(t, f.do(req{path: "/group/open/-/issues/settings", auth: auth}), "Issues here are named OPE-1", `value="OPE"`)
	seeOther(t, f.post("/group/open/-/issues/settings", auth, url.Values{"op": {"label"}, "name": {"bug"}, "color": {"#e5484d"}}), "/group/open/-/issues/settings?done=label-created")
	has(t, f.do(req{path: "/group/open/-/issues/settings", auth: auth}), `fill="#e5484d"`, " bug</span>")
	seeOther(t, f.post("/group/open/-/issues/settings", auth, url.Values{"op": {"key"}, "key": {"open"}}), "/group/open/-/issues/settings?done=key-saved")
	has(t, f.do(req{path: "/group/open/-/issues/1", auth: auth}), "OPEN-1")
	w = f.post("/group/open/-/issues/settings", auth, url.Values{"op": {"key"}, "key": {"9"}})
	has(t, w, "An issue key is 2 to 10 letters")

	// My issues: assigned to me by default, across repositories.
	has(t, f.do(req{path: "/-/issues", auth: auth}), "<h1>My issues</h1>", "Add a board", "group/open")
	hasNot(t, f.do(req{path: "/-/issues", auth: auth}), "Columns, renamed")
	has(t, f.do(req{path: "/-/issues?assignee=&scope=all", auth: auth}), "Columns, renamed")

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
