package web

import (
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/issue"
	"github.com/charmbracelet/soft-serve/pkg/ops"
)

// Project and cycle pages, under a repository's Issues tab:
//
//	/{repo}/-/issues/projects                the projects (POST: add one)
//	/{repo}/-/issues/project?name=NAME       a project and its issues (POST: change it)
//	/{repo}/-/issues/project/delete?name=    delete it (confirm)
//	/{repo}/-/issues/cycles                  the cycles (POST: add the next)
//	/{repo}/-/issues/cycles/N                a cycle and its issues (POST: change it)
//	/{repo}/-/issues/cycles/N/delete         delete it (confirm)
//
// A project's name can hold any character, slashes included, so it goes in
// the query rather than the path.

// groupArgs is what the issuegroups template needs.
type groupArgs struct {
	Groups   []issueGroup
	Colors   map[string]string
	CanWrite bool
	Back     string
	Viewer   string
	Solo     bool
}

// issuesNavArgs is what the switch between issues, projects and cycles
// needs.
type issuesNavArgs struct{ Repo, Current string }

// projectURL is a project's page, by its ID, so a rename doesn't break
// links to it.
func projectURL(repo string, id int64, rest ...string) string {
	return repoURL(repo, "issues/projects/"+strconv.FormatInt(id, 10)+strings.Join(append([]string{""}, rest...), "/"), "", "")
}

// findProject is one of the repository's projects, by ID.
func findProject(r *http.Request, svc ops.IssueService, repo, id string) (ops.IssueProject, error) {
	ps, err := svc.IssueProjects(r.Context(), repo)
	if err != nil {
		return ops.IssueProject{}, err
	}
	for _, p := range ps {
		if strconv.FormatInt(p.ID, 10) == id {
			return p, nil
		}
	}
	return ops.IssueProject{}, errNotFound
}

// projectByName sends an old link by name to the project's page.
func (h uiHandler) projectByName(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	pr, err := b.service().IssueProject(r.Context(), p.Repo.Name, r.URL.Query().Get("name"))
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	http.Redirect(w, r, projectURL(p.Repo.Name, pr.ID), http.StatusMovedPermanently)
}

func cycleURL(repo string, n int64, rest ...string) string {
	return repoURL(repo, "issues/cycles/"+strconv.FormatInt(n, 10)+strings.Join(append([]string{""}, rest...), "/"), "", "")
}

func (b *browser) canWriteIssues(p page) bool {
	return b.user != nil && access.ParseAccessLevel(p.Repo.Access) >= access.ReadWriteAccess
}

// issueGroups lists the issues q selects, grouped by status as the issue
// list groups them.
func issueGroups(r *http.Request, svc ops.IssueService, q ops.IssueQuery) ([]issueGroup, int, error) {
	q.Limit = 500
	l, err := svc.ListIssues(r.Context(), q)
	if err != nil {
		return nil, 0, err
	}
	by := map[string][]ops.Issue{}
	for _, i := range l.Issues {
		by[i.Status] = append(by[i.Status], i)
	}
	var gs []issueGroup
	for _, st := range issueGroupOrder {
		if len(by[st]) > 0 {
			gs = append(gs, issueGroup{Status: st, Issues: by[st]})
		}
	}
	return gs, l.Total, nil
}

// --- Projects ---

type projectsData struct {
	Projects []ops.IssueProject
	Statuses []string
	CanWrite bool
	Solo     bool
	Form     ops.IssueProjectInput
	Err      string
}

func (h uiHandler) issueProjects(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	svc := b.service()
	p.Title = "Projects · " + p.Repo.Name
	p.Tab = "issues"
	p.Solo = b.solo(r)
	d := &projectsData{Statuses: issue.ProjectStatuses, CanWrite: b.canWriteIssues(p), Form: ops.IssueProjectInput{Status: issue.ProjectPlanned}, Solo: p.Solo}
	p.Data = d
	var err error
	if r.Method == http.MethodPost {
		var f url.Values
		if f, err = readForm(w, r); err == nil {
			d.Form = ops.IssueProjectInput{
				Name: f.Get("name"), Description: f.Get("description"), Status: f.Get("status"),
				Lead: strings.TrimSpace(f.Get("lead")), StartDate: f.Get("start_date"), TargetDate: f.Get("target_date"),
			}
			var created ops.IssueProject
			if created, err = svc.CreateIssueProject(r.Context(), p.Repo.Name, d.Form); err == nil {
				http.Redirect(w, r, withDone(projectURL(p.Repo.Name, created.ID), "project-created"), http.StatusSeeOther)
				return
			}
		}
	}
	ps, perr := svc.IssueProjects(r.Context(), p.Repo.Name)
	if perr != nil {
		h.opsFail(w, r, b, p, perr)
		return
	}
	d.Projects = ps
	if err != nil {
		h.renderForm(w, r, b, p, "projects", err, func(m string) { d.Err = m })
		return
	}
	h.render(w, r, "projects", p)
}

type projectData struct {
	Project     ops.IssueProject
	Solo        bool
	Back        string
	Description template.HTML
	Groups      []issueGroup
	Total       int
	Labels      []ops.IssueLabel
	Statuses    []string
	CanWrite    bool
	Form        ops.IssueProjectInput
	Err         string
}

func (h uiHandler) issueProject(w http.ResponseWriter, r *http.Request, b *browser, p page, id, rest string) {
	svc := b.service()
	p.Tab = "issues"
	p.Script, p.Solo = true, b.solo(r)
	pr, err := findProject(r, svc, p.Repo.Name, id)
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	if rest == "delete" {
		h.projectDelete(w, r, b, p, pr)
		return
	}
	p.Title = pr.Name + " · " + p.Repo.Name
	d := &projectData{Project: pr, Statuses: issue.ProjectStatuses, CanWrite: b.canWriteIssues(p), Solo: p.Solo, Back: backOf(r)}
	d.Form = ops.IssueProjectInput{Name: pr.Name, Description: pr.Description, Status: pr.Status, Lead: pr.Lead, StartDate: pr.StartDate, TargetDate: pr.TargetDate}
	p.Data = d
	if r.Method == http.MethodPost {
		var f url.Values
		if f, err = readForm(w, r); err == nil {
			d.Form = ops.IssueProjectInput{
				Name: f.Get("name"), Description: f.Get("description"), Status: f.Get("status"),
				Lead: strings.TrimSpace(f.Get("lead")), StartDate: f.Get("start_date"), TargetDate: f.Get("target_date"),
			}
			var up ops.IssueProject
			up, err = svc.UpdateIssueProject(r.Context(), p.Repo.Name, pr.Name, ops.IssueProjectUpdate{
				Name: &d.Form.Name, Description: &d.Form.Description, Status: &d.Form.Status,
				Lead: &d.Form.Lead, StartDate: &d.Form.StartDate, TargetDate: &d.Form.TargetDate,
			})
			if err == nil {
				http.Redirect(w, r, withDone(projectURL(p.Repo.Name, up.ID), "project-saved"), http.StatusSeeOther)
				return
			}
		}
	}
	d.Description = markdownFor(p.Repo.Name, pr.Description)
	var gerr error
	d.Groups, d.Total, gerr = issueGroups(r, svc, ops.IssueQuery{Repo: p.Repo.Name, Project: pr.Name})
	if gerr != nil {
		h.opsFail(w, r, b, p, gerr)
		return
	}
	if t, terr := svc.IssueTracker(r.Context(), p.Repo.Name); terr == nil {
		d.Labels = t.Labels
	}
	if err != nil {
		h.renderForm(w, r, b, p, "project", err, func(m string) { d.Err = m })
		return
	}
	h.render(w, r, "project", p)
}

func (h uiHandler) projectDelete(w http.ResponseWriter, r *http.Request, b *browser, p page, pr ops.IssueProject) {
	if r.Method == http.MethodPost {
		if _, err := readForm(w, r); err != nil {
			h.opsFail(w, r, b, p, err)
			return
		}
		if err := b.service().DeleteIssueProject(r.Context(), p.Repo.Name, pr.Name); err != nil {
			h.opsFail(w, r, b, p, err)
			return
		}
		http.Redirect(w, r, withDone(repoURL(p.Repo.Name, "issues/projects", "", ""), "project-deleted"), http.StatusSeeOther)
		return
	}
	if !b.canWriteIssues(p) {
		h.opsFail(w, r, b, p, errForbiddenFor(b))
		return
	}
	p.Title = "Delete " + pr.Name
	n := pr.Progress.Total + pr.Progress.Canceled
	msg := "It has no issues."
	if n > 0 {
		msg = "Its " + plural(n, "issue stays", "issues stay") + ", in no project."
	}
	p.Data = confirmData{
		Heading: "Delete this project?",
		What:    pr.Name,
		Message: msg,
		Action:  projectURL(p.Repo.Name, pr.ID, "delete"),
		Button:  "Delete project",
		Cancel:  projectURL(p.Repo.Name, pr.ID),
	}
	h.render(w, r, "confirm", p)
}

// --- Cycles ---

type cyclesData struct {
	Cycles   []ops.IssueCycle
	CanWrite bool
	Form     ops.IssueCycleInput
	Err      string
}

func (h uiHandler) issueCycles(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	svc := b.service()
	p.Title = "Cycles · " + p.Repo.Name
	p.Tab = "issues"
	d := &cyclesData{CanWrite: b.canWriteIssues(p)}
	p.Data = d
	var err error
	if r.Method == http.MethodPost {
		var f url.Values
		if f, err = readForm(w, r); err == nil {
			d.Form = ops.IssueCycleInput{Name: f.Get("name"), StartDate: f.Get("start_date"), EndDate: f.Get("end_date")}
			var c ops.IssueCycle
			if c, err = svc.CreateIssueCycle(r.Context(), p.Repo.Name, d.Form); err == nil {
				http.Redirect(w, r, withDone(cycleURL(p.Repo.Name, c.Number), "cycle-created"), http.StatusSeeOther)
				return
			}
		}
	}
	cs, cerr := svc.IssueCycles(r.Context(), p.Repo.Name)
	if cerr != nil {
		h.opsFail(w, r, b, p, cerr)
		return
	}
	d.Cycles = cs
	if err != nil {
		h.renderForm(w, r, b, p, "cycles", err, func(m string) { d.Err = m })
		return
	}
	h.render(w, r, "cycles", p)
}

type cycleData struct {
	Cycle    ops.IssueCycle
	Solo     bool
	Back     string
	Groups   []issueGroup
	Total    int
	Labels   []ops.IssueLabel
	CanWrite bool
	Form     ops.IssueCycleInput
	Err      string
}

func (h uiHandler) issueCycle(w http.ResponseWriter, r *http.Request, b *browser, p page, ref, rest string) {
	svc := b.service()
	p.Tab = "issues"
	c, err := svc.IssueCycle(r.Context(), p.Repo.Name, ref)
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	num := strconv.FormatInt(c.Number, 10)
	if rest == "delete" {
		if r.Method == http.MethodPost {
			if _, err := readForm(w, r); err != nil {
				h.opsFail(w, r, b, p, err)
				return
			}
			if err := svc.DeleteIssueCycle(r.Context(), p.Repo.Name, num); err != nil {
				h.opsFail(w, r, b, p, err)
				return
			}
			http.Redirect(w, r, withDone(repoURL(p.Repo.Name, "issues/cycles", "", ""), "cycle-deleted"), http.StatusSeeOther)
			return
		}
		if !b.canWriteIssues(p) {
			h.opsFail(w, r, b, p, errForbiddenFor(b))
			return
		}
		n := c.Progress.Total + c.Progress.Canceled
		msg := "It has no issues."
		if n > 0 {
			msg = "Its " + plural(n, "issue stays", "issues stay") + ", in no cycle."
		}
		p.Title = "Delete cycle " + num
		p.Data = confirmData{
			Heading: "Delete this cycle?", What: "Cycle " + num + " (" + c.StartDate + " to " + c.EndDate + ")", Message: msg,
			Action: cycleURL(p.Repo.Name, c.Number, "delete"), Button: "Delete cycle", Cancel: cycleURL(p.Repo.Name, c.Number),
		}
		h.render(w, r, "confirm", p)
		return
	}
	p.Title = "Cycle " + num + " · " + p.Repo.Name
	p.Script, p.Solo = true, b.solo(r)
	d := &cycleData{Cycle: c, CanWrite: b.canWriteIssues(p), Solo: p.Solo, Back: backOf(r), Form: ops.IssueCycleInput{Name: c.Name, StartDate: c.StartDate, EndDate: c.EndDate}}
	p.Data = d
	if r.Method == http.MethodPost {
		var f url.Values
		if f, err = readForm(w, r); err == nil {
			d.Form = ops.IssueCycleInput{Name: f.Get("name"), StartDate: f.Get("start_date"), EndDate: f.Get("end_date")}
			if _, err = svc.UpdateIssueCycle(r.Context(), p.Repo.Name, num, ops.IssueCycleUpdate{Name: &d.Form.Name, StartDate: &d.Form.StartDate, EndDate: &d.Form.EndDate}); err == nil {
				http.Redirect(w, r, withDone(cycleURL(p.Repo.Name, c.Number), "cycle-saved"), http.StatusSeeOther)
				return
			}
		}
	}
	var gerr error
	d.Groups, d.Total, gerr = issueGroups(r, svc, ops.IssueQuery{Repo: p.Repo.Name, Cycle: num})
	if gerr != nil {
		h.opsFail(w, r, b, p, gerr)
		return
	}
	if t, terr := svc.IssueTracker(r.Context(), p.Repo.Name); terr == nil {
		d.Labels = t.Labels
	}
	if err != nil {
		h.renderForm(w, r, b, p, "cycle", err, func(m string) { d.Err = m })
		return
	}
	h.render(w, r, "cycle", p)
}
