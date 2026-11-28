package ops

import (
	"errors"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/issue"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

func TestIssuesAccess(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx

	// Readers see issues but can't write; strangers can't see the
	// repository at all.
	if _, err := f.as(f.reader).CreateIssue(ctx, "secret", IssueCreate{Title: "nope"}); !errors.Is(err, proto.ErrUnauthorized) {
		t.Errorf("reader create: %v", err)
	}
	if _, err := f.as(f.stranger).ListIssues(ctx, IssueQuery{Repo: "secret"}); !errors.Is(err, proto.ErrRepoNotFound) {
		t.Errorf("stranger list: %v", err)
	}
	if _, err := NewLocal(f.be, Actor{}).ListIssues(ctx, IssueQuery{Repo: "secret"}); err == nil {
		t.Error("anonymous list of a private repository")
	}

	is, err := f.as(f.writer).CreateIssue(ctx, "secret", IssueCreate{Title: "  First one\n", Priority: "high", Labels: []string{"bug"}, Assignee: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	if is.ID != "SEC-1" || is.Title != "First one" || is.Status != issue.Todo || is.Priority != "high" || is.Assignee != "reader" || len(is.Labels) != 1 || is.Creator != "writer" {
		t.Errorf("created %+v", is)
	}
	if _, err := f.as(f.writer).CreateIssue(ctx, "secret", IssueCreate{Title: "x", Assignee: "stranger"}); !IsInvalid(err) {
		t.Errorf("assigning someone who can't read: %v", err)
	}
	got, err := f.as(f.reader).Issue(ctx, "secret", 1)
	if err != nil || got.Title != "First one" || len(got.Activity) != 1 || got.Activity[0].Kind != issue.EventCreated {
		t.Errorf("reader get: %+v %v", got, err)
	}
	if _, _, err := f.as(f.stranger).FindIssue(ctx, "SEC-1"); !IsNotFound(err) {
		t.Errorf("stranger find: %v", err)
	}
	if repo, n, err := f.as(f.reader).FindIssue(ctx, "sec-1"); err != nil || repo != "secret" || n != 1 {
		t.Errorf("find: %s %d %v", repo, n, err)
	}
	if err := f.as(f.writer).SetIssueKey(ctx, "secret", "SS"); !errors.Is(err, proto.ErrUnauthorized) {
		t.Errorf("writer set key: %v", err)
	}
}

func TestIssuesLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	w := f.as(f.writer)

	parent, err := w.CreateIssue(ctx, "secret", IssueCreate{Title: "Parent", Status: "backlog"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := w.CreateIssue(ctx, "secret", IssueCreate{Title: "Child", Parent: parent.ID, Status: "In progress"})
	if err != nil {
		t.Fatal(err)
	}
	if child.Parent != "SEC-1" || child.Status != issue.InProgress || child.StartedAt == nil {
		t.Errorf("child %+v", child)
	}
	if _, err := w.UpdateIssue(ctx, "secret", 1, IssueUpdate{Parent: ptr("SEC-2")}); !IsInvalid(err) {
		t.Errorf("a cycle of parents: %v", err)
	}

	up, err := w.UpdateIssue(ctx, "secret", 2, IssueUpdate{
		Status: ptr("done"), Priority: ptr("urgent"), Assignee: ptr("me"),
		AddLabels: []string{"web", "bug"}, Title: ptr("Child, renamed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if up.Status != issue.Done || up.CompletedAt == nil || up.Priority != "urgent" || up.Assignee != "writer" || len(up.Labels) != 2 || up.Title != "Child, renamed" {
		t.Errorf("updated %+v", up)
	}
	up, err = w.UpdateIssue(ctx, "secret", 2, IssueUpdate{Labels: &[]string{"web"}, Assignee: ptr("")})
	if err != nil || len(up.Labels) != 1 || up.Labels[0] != "web" || up.Assignee != "" {
		t.Errorf("replace labels: %+v %v", up, err)
	}

	c, err := w.AddIssueComment(ctx, "secret", 2, "Looks good.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.as(f.owner).UpdateIssueComment(ctx, "secret", 2, c.ID, "owner edits"); err != nil {
		t.Errorf("owner edits a comment: %v", err)
	}
	other := f.as(f.admin)
	if _, err := other.UpdateIssueComment(ctx, "secret", 2, c.ID, "repo admin edits"); err != nil {
		t.Errorf("repo admin edits a comment: %v", err)
	}
	if _, err := f.as(f.reader).AddIssueComment(ctx, "secret", 2, "hi"); !errors.Is(err, proto.ErrUnauthorized) {
		t.Errorf("reader comments: %v", err)
	}

	d, err := w.Issue(ctx, "secret", 1)
	if err != nil || len(d.SubIssues) != 1 || d.SubIssues[0].ID != "SEC-2" {
		t.Errorf("parent detail: %+v %v", d.SubIssues, err)
	}
	d, err = w.Issue(ctx, "secret", 2)
	if err != nil || len(d.Comments) != 1 || d.Comments[0].Body != "repo admin edits" {
		t.Errorf("child detail comments: %+v %v", d.Comments, err)
	}
	kinds := map[string]int{}
	for _, e := range d.Activity {
		kinds[e.Kind]++
	}
	if kinds[issue.EventStatus] != 1 || kinds[issue.EventLabelAdded] != 2 || kinds[issue.EventLabelRemove] != 1 || kinds[issue.EventAssignee] != 2 {
		t.Errorf("activity: %v", kinds)
	}

	// Lists: open by default, closed on request, and filters.
	l, err := w.ListIssues(ctx, IssueQuery{Repo: "secret"})
	if err != nil || l.Total != 1 || l.Issues[0].ID != "SEC-1" {
		t.Errorf("open: %+v %v", l, err)
	}
	l, err = w.ListIssues(ctx, IssueQuery{Repo: "secret", Closed: true, Label: []string{"WEB"}})
	if err != nil || l.Total != 1 || l.Issues[0].ID != "SEC-2" {
		t.Errorf("label: %+v %v", l, err)
	}
	l, err = w.ListIssues(ctx, IssueQuery{Query: "renamed", Closed: true})
	if err != nil || l.Total != 1 {
		t.Errorf("query across repositories: %+v %v", l, err)
	}
	l, err = w.ListIssues(ctx, IssueQuery{Query: "SEC-1"})
	if err != nil || l.Total != 1 || l.Issues[0].Number != 1 {
		t.Errorf("query by identifier: %+v %v", l, err)
	}
	l, err = w.ListIssues(ctx, IssueQuery{Repo: "secret", Parent: "1"})
	if err != nil || l.Total != 1 {
		t.Errorf("sub-issues: %+v %v", l, err)
	}
	if _, err := w.ListIssues(ctx, IssueQuery{Repo: "secret", Status: []string{"nope"}}); !IsInvalid(err) {
		t.Errorf("bad status: %v", err)
	}
	if l, _ := f.as(f.stranger).ListIssues(ctx, IssueQuery{Closed: true}); l.Total != 0 {
		t.Errorf("a stranger sees %d issues across repositories", l.Total)
	}

	// The key can change, and identifiers follow it.
	if err := f.as(f.admin).SetIssueKey(ctx, "secret", "top"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.as(f.owner).CreateIssue(ctx, "shy", IssueCreate{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := f.as(f.owner).SetIssueKey(ctx, "shy", "TOP"); !IsInvalid(err) {
		t.Errorf("a key in use: %v", err)
	}
	d, err = w.Issue(ctx, "secret", 2)
	if err != nil || d.ID != "TOP-2" || d.Parent != "TOP-1" {
		t.Errorf("after rekey: %s %s %v", d.ID, d.Parent, err)
	}

	// Labels.
	if _, err := w.CreateIssueLabel(ctx, "secret", IssueLabel{Name: "Web"}); !IsInvalid(err) {
		t.Errorf("duplicate label: %v", err)
	}
	if _, err := w.UpdateIssueLabel(ctx, "secret", "web", IssueLabelUpdate{Name: ptr("frontend"), Color: ptr("#E5484D")}); err != nil {
		t.Fatal(err)
	}
	ls, _ := w.IssueLabels(ctx, "secret")
	if len(ls) != 2 || ls[1].Name != "frontend" || ls[1].Color != "#e5484d" {
		t.Errorf("labels: %+v", ls)
	}
	if err := w.DeleteIssueLabel(ctx, "secret", "frontend"); err != nil {
		t.Fatal(err)
	}

	if err := w.DeleteIssue(ctx, "secret", 1); err != nil {
		t.Fatal(err)
	}
	d, err = w.Issue(ctx, "secret", 2)
	if err != nil || d.Parent != "" || len(d.Labels) != 0 {
		t.Errorf("after deleting the parent and the label: %+v %v", d.Issue, err)
	}
	if _, err := w.Issue(ctx, "secret", 1); !IsNotFound(err) {
		t.Errorf("deleted issue: %v", err)
	}
}

func TestIssueRefs(t *testing.T) {
	refs := issue.ParseMessage("Fixes ICE-12, ICE-13 and ICE-14\n\nSee also SS-2 and utf-8. Closes: ICE-12")
	want := map[string]bool{"ICE-12": true, "ICE-13": true, "ICE-14": true, "SS-2": false}
	if len(refs) != len(want) {
		t.Fatalf("refs = %+v", refs)
	}
	for _, r := range refs {
		if c, ok := want[r.ID()]; !ok || c != r.Closes {
			t.Errorf("%s closes=%v", r.ID(), r.Closes)
		}
	}
	if b := issue.ParseBranch("sroberts/ice-12-fix-it"); len(b) != 1 || b[0].ID() != "ICE-12" {
		t.Errorf("branch refs = %+v", b)
	}
	for in, want := range map[string]string{"icecream": "ICE", "soft-serve": "SS", "org/nested-sundae": "NS", "9lives": "R9LI", "a": "AX"} {
		if got := issue.DeriveKey(in); got != want {
			t.Errorf("DeriveKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIssueOrder(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	w := f.as(f.writer)
	for _, title := range []string{"a", "b", "c", "d"} {
		if _, err := w.CreateIssue(ctx, "secret", IssueCreate{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	order := func() string {
		l, err := w.ListIssues(ctx, IssueQuery{Repo: "secret", Sort: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		s := ""
		for _, i := range l.Issues {
			s += i.Title
		}
		return s
	}
	if got := order(); got != "abcd" {
		t.Fatalf("created in order: %s", got)
	}
	if _, err := w.UpdateIssue(ctx, "secret", 4, IssueUpdate{After: ptr("top")}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpdateIssue(ctx, "secret", 1, IssueUpdate{After: ptr("SEC-3")}); err != nil {
		t.Fatal(err)
	}
	if got := order(); got != "dbca" {
		t.Errorf("after moves: %s", got)
	}
	// Many moves into the same gap stay in order.
	for range 60 {
		if _, err := w.UpdateIssue(ctx, "secret", 1, IssueUpdate{After: ptr("4")}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.UpdateIssue(ctx, "secret", 2, IssueUpdate{After: ptr("4")}); err != nil {
			t.Fatal(err)
		}
	}
	if got := order(); got != "dbac" {
		t.Errorf("after many moves: %s", got)
	}
	// Moving to another column places it there.
	if _, err := w.UpdateIssue(ctx, "secret", 3, IssueUpdate{Status: ptr("done"), After: ptr("4")}); !IsInvalid(err) {
		t.Errorf("after an issue in another column: %v", err)
	}
	if _, err := w.UpdateIssue(ctx, "secret", 3, IssueUpdate{Status: ptr("done"), After: ptr("top")}); err != nil {
		t.Error(err)
	}
}
