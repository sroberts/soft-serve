package ops

import (
	"errors"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/proto"
)

func TestIssueProjects(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	w := f.as(f.writer)

	if _, err := f.as(f.reader).CreateIssueProject(ctx, "secret", IssueProjectInput{Name: "x"}); !errors.Is(err, proto.ErrUnauthorized) {
		t.Errorf("reader creates a project: %v", err)
	}
	p, err := w.CreateIssueProject(ctx, "secret", IssueProjectInput{Name: " Web view v2 ", Lead: "me", TargetDate: "2026-12-15"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Web view v2" || p.Status != "planned" || p.Lead != "writer" || p.TargetDate != "2026-12-15" {
		t.Errorf("created %+v", p)
	}
	for _, in := range []IssueProjectInput{
		{Name: "web VIEW v2"},
		{Name: "Dates", TargetDate: "15/12/2026"},
		{Name: "Backwards", StartDate: "2026-12-15", TargetDate: "2026-12-01"},
		{Name: "Status", Status: "someday"},
	} {
		if _, err := w.CreateIssueProject(ctx, "secret", in); !IsInvalid(err) {
			t.Errorf("%+v: %v", in, err)
		}
	}

	// Issues in it count toward its progress; canceled ones don't.
	for _, st := range []string{"done", "done", "in_progress", "todo", "canceled"} {
		if _, err := w.CreateIssue(ctx, "secret", IssueCreate{Title: st, Status: st, Project: "web view v2"}); err != nil {
			t.Fatal(err)
		}
	}
	p, err = f.as(f.reader).IssueProject(ctx, "secret", "Web view v2")
	if err != nil {
		t.Fatal(err)
	}
	if p.Progress != (IssueProgress{Total: 4, Started: 1, Completed: 2, Canceled: 1, Percent: 50}) {
		t.Errorf("progress %+v", p.Progress)
	}
	l, err := w.ListIssues(ctx, IssueQuery{Repo: "secret", Project: "Web view v2"})
	if err != nil || l.Total != 5 {
		t.Errorf("issues in the project, closed included: %d %v", l.Total, err)
	}
	if _, err := w.ListIssues(ctx, IssueQuery{Project: "Web view v2"}); !IsInvalid(err) {
		t.Errorf("project filter across repositories: %v", err)
	}
	if _, err := w.CreateIssue(ctx, "secret", IssueCreate{Title: "x", Project: "nope"}); !IsNotFound(err) {
		t.Errorf("unknown project: %v", err)
	}

	// Rename, then move an issue out; the activity says so.
	if _, err := w.UpdateIssueProject(ctx, "secret", "web view v2", IssueProjectUpdate{Name: ptr("Web view 2"), Status: ptr("started")}); err != nil {
		t.Fatal(err)
	}
	up, err := w.UpdateIssue(ctx, "secret", 4, IssueUpdate{Project: ptr("")})
	if err != nil || up.Project != "" {
		t.Errorf("out of the project: %+v %v", up, err)
	}
	d, _ := w.Issue(ctx, "secret", 4)
	if last := d.Activity[len(d.Activity)-1]; last.Kind != "project" || last.From != "Web view 2" || last.To != "" {
		t.Errorf("activity: %+v", last)
	}

	// Deleting keeps the issues.
	if err := w.DeleteIssueProject(ctx, "secret", "Web view 2"); err != nil {
		t.Fatal(err)
	}
	if d, err := w.Issue(ctx, "secret", 1); err != nil || d.Project != "" {
		t.Errorf("issue after its project went: %+v %v", d.Issue, err)
	}
}

func TestIssueCycles(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	w := f.as(f.writer)
	defer func(old func() string) { today = old }(today)
	today = func() string { return "2026-11-20" }

	c1, err := w.CreateIssueCycle(ctx, "secret", IssueCycleInput{StartDate: "2026-11-16"})
	if err != nil {
		t.Fatal(err)
	}
	if c1.Number != 1 || c1.EndDate != "2026-11-29" || !c1.Current {
		t.Errorf("cycle 1 %+v", c1)
	}
	// The next one follows on by itself.
	c2, err := w.CreateIssueCycle(ctx, "secret", IssueCycleInput{Name: "Polish"})
	if err != nil || c2.Number != 2 || c2.StartDate != "2026-11-30" || c2.EndDate != "2026-12-13" || c2.Current {
		t.Errorf("cycle 2 %+v %v", c2, err)
	}
	if _, err := w.CreateIssueCycle(ctx, "secret", IssueCycleInput{StartDate: "2026-12-01", EndDate: "2026-12-05"}); !IsInvalid(err) {
		t.Errorf("an overlapping cycle: %v", err)
	}
	if _, err := w.CreateIssueCycle(ctx, "secret", IssueCycleInput{StartDate: "2027-01-10", EndDate: "2027-01-01"}); !IsInvalid(err) {
		t.Errorf("a backwards cycle: %v", err)
	}

	// current and next name them by date.
	if _, err := w.CreateIssue(ctx, "secret", IssueCreate{Title: "now", Cycle: "current", Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateIssue(ctx, "secret", IssueCreate{Title: "soon", Cycle: "next"}); err != nil {
		t.Fatal(err)
	}
	cur, err := f.as(f.reader).IssueCycle(ctx, "secret", "current")
	if err != nil || cur.Number != 1 || cur.Progress.Completed != 1 {
		t.Errorf("current: %+v %v", cur, err)
	}
	l, err := w.ListIssues(ctx, IssueQuery{Repo: "secret", Cycle: "next"})
	if err != nil || l.Total != 1 || l.Issues[0].Cycle != 2 {
		t.Errorf("next cycle's issues: %+v %v", l, err)
	}
	if _, err := w.IssueCycle(ctx, "secret", "previous"); !IsNotFound(err) {
		t.Errorf("no previous cycle yet: %v", err)
	}
	up, err := w.UpdateIssue(ctx, "secret", 2, IssueUpdate{Cycle: ptr("1")})
	if err != nil || up.Cycle != 1 {
		t.Errorf("move to cycle 1: %+v %v", up, err)
	}
	if _, err := w.UpdateIssueCycle(ctx, "secret", "2", IssueCycleUpdate{EndDate: ptr("2026-12-20")}); err != nil {
		t.Errorf("extend cycle 2: %v", err)
	}
	if err := w.DeleteIssueCycle(ctx, "secret", "1"); err != nil {
		t.Fatal(err)
	}
	if d, err := w.Issue(ctx, "secret", 1); err != nil || d.Cycle != 0 {
		t.Errorf("issue after its cycle went: %+v %v", d.Issue, err)
	}
	cs, _ := w.IssueCycles(ctx, "secret")
	if len(cs) != 1 || cs[0].Number != 2 {
		t.Errorf("cycles left: %+v", cs)
	}
}
