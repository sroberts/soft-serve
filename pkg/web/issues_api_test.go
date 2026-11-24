package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/ops"
)

func TestAPIIssues(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()

	// Create, then read it back by number and by identifier.
	w := f.do(req{
		method: http.MethodPost, path: "/v1/repos/group/open/-/issues", auth: auth,
		body: `{"title":"Clone URLs say localhost","priority":"high","labels":["bug"],"assignee":"me"}`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	created := decodeJSON[ops.Issue](t, w.Body.Bytes())
	if created.ID != "OPE-1" || created.Assignee != "owner" || created.Priority != "high" {
		t.Errorf("created %+v", created)
	}
	d := decodeJSON[ops.IssueDetail](t, f.do(req{path: "/v1/repos/group/open/-/issues/1", auth: auth}).Body.Bytes())
	if d.Title != "Clone URLs say localhost" || len(d.Activity) != 1 {
		t.Errorf("detail %+v", d)
	}
	if w := f.do(req{path: "/v1/issues/ope-1", auth: auth}); w.Code != http.StatusOK {
		t.Errorf("by identifier: %d", w.Code)
	}

	// Change it, comment, list.
	w = f.do(req{method: http.MethodPatch, path: "/v1/issues/OPE-1", auth: auth, body: `{"status":"in_progress","add_labels":["web"]}`})
	if up := decodeJSON[ops.Issue](t, w.Body.Bytes()); up.Status != "in_progress" || len(up.Labels) != 2 {
		t.Errorf("patch: %d %+v", w.Code, up)
	}
	w = f.do(req{method: http.MethodPost, path: "/v1/issues/OPE-1/comments", auth: auth, body: `{"body":"On it."}`})
	if w.Code != http.StatusCreated {
		t.Errorf("comment: %d %s", w.Code, w.Body)
	}
	l := decodeJSON[ops.IssueList](t, f.do(req{path: "/v1/issues?assignee=me&label=web", auth: auth}).Body.Bytes())
	if l.Total != 1 || l.Issues[0].CommentCount != 1 {
		t.Errorf("list: %+v", l)
	}
	l = decodeJSON[ops.IssueList](t, f.do(req{path: "/v1/repos/group/open/-/issues?status=done", auth: auth}).Body.Bytes())
	if l.Total != 0 {
		t.Errorf("done: %+v", l)
	}

	// The caller's mistakes say what's wrong; missing issues are 404.
	if w := f.do(req{method: http.MethodPatch, path: "/v1/issues/OPE-1", auth: auth, body: `{"status":"shipped"}`}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unknown status") {
		t.Errorf("bad status: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{path: "/v1/issues/OPE-99", auth: auth}); w.Code != http.StatusNotFound {
		t.Errorf("missing issue: %d", w.Code)
	}
	if w := f.do(req{path: "/v1/issues/ZZZ-1", auth: auth}); w.Code != http.StatusNotFound {
		t.Errorf("unknown key: %d", w.Code)
	}

	// Anonymous callers read a public repository's issues but can't write,
	// and see nothing of a private one's.
	if w := f.do(req{path: "/v1/repos/group/open/-/issues"}); w.Code != http.StatusOK {
		t.Errorf("anonymous read: %d", w.Code)
	}
	if w := f.do(req{method: http.MethodPost, path: "/v1/repos/group/open/-/issues", body: `{"title":"x"}`}); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous create: %d", w.Code)
	}
	f.do(req{method: http.MethodPost, path: "/v1/repos/secret/-/issues", auth: auth, body: `{"title":"Hidden plans"}`})
	if w := f.do(req{path: "/v1/issues/SEC-1"}); w.Code == http.StatusOK {
		t.Errorf("anonymous read of a private repository's issue: %d", w.Code)
	}
	if l := decodeJSON[ops.IssueList](t, f.do(req{path: "/v1/issues"}).Body.Bytes()); l.Total != 1 {
		t.Errorf("anonymous list across repositories: %+v", l)
	}

	// Tracker and labels.
	w = f.do(req{method: http.MethodPatch, path: "/v1/repos/group/open/-/issue-tracker", auth: auth, body: `{"key":"open"}`})
	if tr := decodeJSON[ops.IssueTracker](t, w.Body.Bytes()); tr.Key != "OPEN" || tr.Open != 1 || len(tr.Statuses) != 6 || len(tr.Labels) != 2 {
		t.Errorf("tracker: %d %+v", w.Code, tr)
	}
	if w := f.do(req{method: http.MethodPatch, path: "/v1/repos/group/open/-/labels/web", auth: auth, body: `{"color":"#123456"}`}); w.Code != http.StatusOK {
		t.Errorf("label patch: %d %s", w.Code, w.Body)
	}
	if w := f.do(req{method: http.MethodDelete, path: "/v1/repos/group/open/-/labels/web", auth: auth}); w.Code != http.StatusNoContent {
		t.Errorf("label delete: %d", w.Code)
	}
	if w := f.do(req{method: http.MethodDelete, path: "/v1/issues/OPEN-1", auth: auth}); w.Code != http.StatusNoContent {
		t.Errorf("delete: %d", w.Code)
	}
}

func TestMCPIssues(t *testing.T) {
	f := newViewFixture(t)
	call := func(auth, name, args string) (bool, map[string]any, string) {
		t.Helper()
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
		hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(body))
		hr.Host = "localhost"
		hr.Header.Set("Content-Type", "application/json")
		hr.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			hr.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, hr)
		var out struct {
			Result struct {
				IsError           bool             `json:"isError"`
				StructuredContent map[string]any   `json:"structuredContent"`
				Content           []map[string]any `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v %s", name, err, w.Body)
		}
		text := ""
		if len(out.Result.Content) > 0 {
			text, _ = out.Result.Content[0]["text"].(string)
		}
		return out.Result.IsError, out.Result.StructuredContent, text
	}
	ok := func(auth, name, args string) map[string]any {
		t.Helper()
		isErr, res, text := call(auth, name, args)
		if isErr {
			t.Fatalf("%s: %s", name, text)
		}
		return res
	}
	owner := f.owner()

	c := ok(owner, "create_issue", `{"repo":"group/open","title":"Add a board","priority":"urgent","labels":["ui"]}`)
	if c["id"] != "OPE-1" || c["status"] != "todo" {
		t.Errorf("create_issue: %v", c)
	}
	ok(owner, "create_issue", `{"repo":"group/open","title":"Columns","parent":"OPE-1"}`)
	u := ok(owner, "update_issue", `{"id":"OPE-2","status":"in_review","assignee":"me"}`)
	if u["status"] != "in_review" || u["assignee"] != "owner" {
		t.Errorf("update_issue: %v", u)
	}
	ok(owner, "comment_on_issue", `{"repo":"group/open","number":1,"body":"Needs drag and drop."}`)
	g := ok(owner, "get_issue", `{"id":"OPE-1"}`)
	if subs, _ := g["sub_issues"].([]any); len(subs) != 1 {
		t.Errorf("get_issue sub-issues: %v", g["sub_issues"])
	}
	if cs, _ := g["comments"].([]any); len(cs) != 1 {
		t.Errorf("get_issue comments: %v", g["comments"])
	}
	l := ok(owner, "list_issues", `{"assignee":"me"}`)
	if l["total"] != float64(1) {
		t.Errorf("list_issues mine: %v", l)
	}
	tr := ok(owner, "get_issue_tracker", `{"repo":"group/open"}`)
	if tr["key"] != "OPE" || tr["open"] != float64(2) {
		t.Errorf("get_issue_tracker: %v", tr)
	}

	// Mistakes come back as messages the model can act on, not as
	// internal errors.
	for _, tc := range []struct{ auth, name, args, want string }{
		{owner, "update_issue", `{"id":"OPE-1","status":"shipped"}`, "unknown status"},
		{owner, "get_issue", `{"id":"OPE-99"}`, "not found"},
		{owner, "get_issue", `{}`, "give the issue's id"},
		{"", "create_issue", `{"repo":"group/open","title":"x"}`, "write access"},
	} {
		isErr, _, text := call(tc.auth, tc.name, tc.args)
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%s %s: error=%v %q, want %q", tc.name, tc.args, isErr, text, tc.want)
		}
	}

	d := ok(owner, "delete_issue", `{"id":"OPE-2"}`)
	if d["deleted"] != "OPE-2" {
		t.Errorf("delete_issue: %v", d)
	}
}

// The OpenAPI document parses, and describes the issue routes.
func TestOpenAPIIssues(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(openAPIDoc, &doc); err != nil {
		t.Fatal(err)
	}
	for path, methods := range map[string][]string{
		"/v1/issues":                         {"get"},
		"/v1/issues/{id}":                    {"get", "patch", "delete"},
		"/v1/repos/{repo}/-/issues":          {"get", "post"},
		"/v1/repos/{repo}/-/issues/{number}": {"get", "patch", "delete"},
		"/v1/repos/{repo}/-/issue-tracker":   {"get", "patch"},
		"/v1/repos/{repo}/-/labels/{label}":  {"patch", "delete"},
	} {
		for _, m := range methods {
			if doc.Paths[path][m] == nil {
				t.Errorf("openapi.json has no %s %s", m, path)
			}
		}
	}
}
