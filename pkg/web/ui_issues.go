package web

import (
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/issue"
	"github.com/charmbracelet/soft-serve/pkg/ops"
)

// Issue pages: a repository's list, one issue, the forms that change them,
// and "My issues" across repositories at /-/issues. Everything goes through
// ops.IssueService, as the API, MCP and SSH do.
//
//	/{repo}/-/issues                         the list (scope, filters, search)
//	/{repo}/-/issues/new                     new issue (GET form, POST create)
//	/{repo}/-/issues/N                       an issue (POST: its properties)
//	/{repo}/-/issues/N/edit                  title and description
//	/{repo}/-/issues/N/comment               add a comment (POST)
//	/{repo}/-/issues/N/comments/ID/edit      change a comment
//	/{repo}/-/issues/N/comments/ID/delete    delete a comment (confirm)
//	/{repo}/-/issues/N/delete                delete the issue (confirm)
//	/{repo}/-/issues/settings                the key and labels
//	/-/issues                                issues across repositories

// issueScopes are the list's tabs: which statuses each shows.
var issueScopes = []struct {
	Name, Label string
	Statuses    []string
	Closed      bool
}{
	{"open", "Open", nil, false},
	{"active", "Active", []string{issue.Todo, issue.InProgress, issue.InReview}, false},
	{"backlog", "Backlog", []string{issue.Backlog}, false},
	{"closed", "Closed", []string{issue.Done, issue.Canceled}, false},
	{"all", "All", nil, true},
}

// issueGroupOrder is the order status groups are listed in: what's moving
// first, as Linear lists them.
var issueGroupOrder = []string{issue.InReview, issue.InProgress, issue.Todo, issue.Backlog, issue.Done, issue.Canceled}

const issuePageSize = 100

type issueScopeLink struct {
	Label, URL string
	Current    bool
}

type issueGroup struct {
	Status string
	Issues []ops.Issue
}

type issueListData struct {
	// Global is the list across repositories.
	Global   bool
	Scopes   []issueScopeLink
	Groups   []issueGroup
	Total    int
	Shown    int
	PrevURL  string
	NextURL  string
	Filtered bool
	ClearURL string
	// The filters, as the form shows them.
	Scope, Query, Assignee, Priority, Label, Sort string
	Project, Cycle                                string
	Labels                                        []ops.IssueLabel
	Projects                                      []ops.IssueProject
	Cycles                                        []ops.IssueCycle
	Priorities                                    []string
	CanWrite                                      bool
	Key                                           string
}

// issues routes the pages under /{repo}/-/issues.
func (h uiHandler) issues(w http.ResponseWriter, r *http.Request, b *browser, p page, rest string) {
	p.Tab = "issues"
	parts := strings.Split(rest, "/")
	switch {
	case rest == "":
		if r.Method == http.MethodPost {
			renderNotFound(w, r)
			return
		}
		h.issueList(w, r, b, p, false)
	case rest == "new":
		h.issueNew(w, r, b, p)
	case rest == "settings":
		h.issueSettings(w, r, b, p)
	case rest == "projects":
		h.issueProjects(w, r, b, p)
	case rest == "project" || rest == "project/delete":
		h.issueProject(w, r, b, p, strings.TrimPrefix(strings.TrimPrefix(rest, "project"), "/"))
	case rest == "cycles":
		h.issueCycles(w, r, b, p)
	case parts[0] == "cycles" && (len(parts) == 2 || (len(parts) == 3 && parts[2] == "delete")):
		h.issueCycle(w, r, b, p, parts[1], strings.Join(parts[2:], ""))
	default:
		n, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || n < 1 {
			h.fail(w, r, p, errNotFound)
			return
		}
		switch {
		case len(parts) == 1:
			h.issuePage(w, r, b, p, n)
		case len(parts) == 2 && parts[1] == "edit":
			h.issueEdit(w, r, b, p, n)
		case len(parts) == 2 && parts[1] == "comment":
			h.issueComment(w, r, b, p, n)
		case len(parts) == 2 && parts[1] == "delete":
			h.issueDelete(w, r, b, p, n)
		case len(parts) == 4 && parts[1] == "comments" && (parts[3] == "edit" || parts[3] == "delete"):
			id, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil {
				h.fail(w, r, p, errNotFound)
				return
			}
			h.issueCommentChange(w, r, b, p, n, id, parts[3])
		default:
			h.fail(w, r, p, errNotFound)
		}
	}
}

// issuesURL is the list's URL with a query.
func issuesURL(repo string, q url.Values) string {
	u := "/-/issues"
	if repo != "" {
		u = repoURL(repo, "issues", "", "")
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	return u
}

// idURL is the page of an issue named by its identifier, in repo (a
// parent is always in the same repository).
func idURL(repo, id string) string {
	_, n, ok := issue.ParseID(id)
	if !ok {
		return repoURL(repo, "issues", "", "")
	}
	return issueURL(repo, n)
}

func issueURL(repo string, number int64, rest ...string) string {
	return repoURL(repo, "issues/"+strconv.FormatInt(number, 10)+strings.Join(append([]string{""}, rest...), "/"), "", "")
}

// issueList is a repository's issues, or with global set, every readable
// repository's, mine by default.
func (h uiHandler) issueList(w http.ResponseWriter, r *http.Request, b *browser, p page, global bool) {
	svc := b.service()
	in := r.URL.Query()
	d := &issueListData{
		Global: global, Scope: in.Get("scope"), Query: strings.TrimSpace(in.Get("q")),
		Assignee: in.Get("assignee"), Priority: in.Get("priority"), Label: in.Get("label"), Sort: in.Get("sort"),
		Project: in.Get("project"), Cycle: in.Get("cycle"),
		Priorities: issue.PriorityNames,
	}
	if global && !in.Has("assignee") && b.user != nil {
		d.Assignee = "me"
	}
	q := ops.IssueQuery{Query: d.Query, Assignee: d.Assignee, Sort: d.Sort, Limit: issuePageSize}
	if d.Priority != "" {
		q.Priority = []string{d.Priority}
	}
	if d.Label != "" {
		q.Label = []string{d.Label}
	}
	scope := issueScopes[0]
	for _, s := range issueScopes {
		if s.Name == d.Scope {
			scope = s
		}
	}
	d.Scope = scope.Name
	q.Status, q.Closed = scope.Statuses, scope.Closed
	q.Offset, _ = strconv.Atoi(in.Get("offset"))
	q.Offset = max(q.Offset, 0)

	repo := ""
	if !global {
		repo = p.Repo.Name
		q.Repo = repo
		q.Project, q.Cycle = d.Project, d.Cycle
		d.Projects, _ = svc.IssueProjects(r.Context(), repo)
		d.Cycles, _ = svc.IssueCycles(r.Context(), repo)
		t, err := svc.IssueTracker(r.Context(), repo)
		if err != nil {
			h.opsFail(w, r, b, p, err)
			return
		}
		d.Labels, d.Key = t.Labels, t.Key
		d.CanWrite = b.user != nil && access.ParseAccessLevel(p.Repo.Access) >= access.ReadWriteAccess
		p.Title = "Issues · " + repo
	} else {
		p.Title = "Issues"
		if d.Assignee == "me" {
			p.Title = "My issues"
		}
	}

	l, err := svc.ListIssues(r.Context(), q)
	if err != nil {
		if ops.IsInvalid(err) {
			// A bad filter in the URL: show the list without it.
			http.Redirect(w, r, issuesURL(repo, nil), http.StatusSeeOther)
			return
		}
		h.opsFail(w, r, b, p, err)
		return
	}
	d.Total, d.Shown = l.Total, len(l.Issues)
	if global {
		// Labels are each repository's own; color them from the
		// repositories on this page, and offer their names to filter by.
		seen := map[string]bool{}
		names := map[string]bool{}
		for _, i := range l.Issues {
			if seen[i.Repo] {
				continue
			}
			seen[i.Repo] = true
			ls, err := svc.IssueLabels(r.Context(), i.Repo)
			if err != nil {
				continue
			}
			for _, lb := range ls {
				if key := strings.ToLower(lb.Name); !names[key] {
					names[key] = true
					d.Labels = append(d.Labels, lb)
				}
			}
		}
		sort.Slice(d.Labels, func(i, j int) bool { return strings.ToLower(d.Labels[i].Name) < strings.ToLower(d.Labels[j].Name) })
	}

	keep := func(change func(url.Values)) string {
		v := url.Values{}
		for k, vs := range in {
			if k != "offset" && k != "done" && len(vs) > 0 && vs[0] != "" {
				v.Set(k, vs[0])
			}
		}
		if global && !in.Has("assignee") && d.Assignee != "" {
			v.Set("assignee", d.Assignee)
		}
		change(v)
		return issuesURL(repo, v)
	}
	for _, s := range issueScopes {
		name := s.Name
		d.Scopes = append(d.Scopes, issueScopeLink{Label: s.Label, Current: name == d.Scope, URL: keep(func(v url.Values) {
			if name == "open" {
				v.Del("scope")
			} else {
				v.Set("scope", name)
			}
		})})
	}
	if q.Offset > 0 {
		d.PrevURL = keep(func(v url.Values) {
			if o := q.Offset - issuePageSize; o > 0 {
				v.Set("offset", strconv.Itoa(o))
			}
		})
	}
	if q.Offset+len(l.Issues) < l.Total {
		d.NextURL = keep(func(v url.Values) { v.Set("offset", strconv.Itoa(q.Offset+issuePageSize)) })
	}
	d.Filtered = d.Query != "" || d.Priority != "" || d.Label != "" || d.Project != "" || d.Cycle != "" || (d.Assignee != "" && !(global && d.Assignee == "me"))
	d.ClearURL = issuesURL(repo, url.Values{"scope": {d.Scope}})
	if d.Scope == "open" {
		d.ClearURL = issuesURL(repo, nil)
	}

	// Group by status, keeping the list's order within each group. A
	// manual or by-number sort reads better ungrouped.
	if d.Sort == "manual" || d.Sort == "number" {
		d.Groups = []issueGroup{{Issues: l.Issues}}
	} else {
		by := map[string][]ops.Issue{}
		for _, i := range l.Issues {
			by[i.Status] = append(by[i.Status], i)
		}
		for _, st := range issueGroupOrder {
			if len(by[st]) > 0 {
				d.Groups = append(d.Groups, issueGroup{Status: st, Issues: by[st]})
			}
		}
	}
	p.Data = d
	h.render(w, r, "issues", p)
}

// myIssues is /-/issues: issues across every repository the visitor can
// read, theirs by default.
func (h uiHandler) myIssues(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "myissues"
	h.issueList(w, r, b, p, true)
}

// --- One issue ---

type timelineItem struct {
	At      time.Time
	Event   *ops.IssueEvent
	Comment *ops.IssueComment
	Body    template.HTML
	CanEdit bool
}

type issueData struct {
	Issue       ops.IssueDetail
	Description template.HTML
	Timeline    []timelineItem
	CanWrite    bool
	// Labels are the repository's, for the property form.
	Labels     []ops.IssueLabel
	Statuses   []string
	Priorities []string
	// Users are suggestions for the assignee field.
	Users []string
	// Key is the repository's issue key, for hints.
	Key string
	// Projects and Cycles are the repository's, for the property form.
	Projects []ops.IssueProject
	Cycles   []ops.IssueCycle
	// Props is the property form as last submitted, and Err why it failed.
	Props        issueProps
	Err          string
	CommentErr   string
	CommentDraft string
}

// issueProps is the property form.
type issueProps struct {
	Status, Priority, Assignee, Parent string
	Project, Cycle                     string
	Labels                             []string
	NewLabel                           string
}

func (p issueProps) Has(label string) bool {
	return slices.ContainsFunc(p.Labels, func(l string) bool { return strings.EqualFold(l, label) })
}

func propsOf(i ops.Issue) issueProps {
	cycle := ""
	if i.Cycle != 0 {
		cycle = strconv.FormatInt(i.Cycle, 10)
	}
	return issueProps{Status: i.Status, Priority: i.Priority, Assignee: i.Assignee, Parent: i.Parent, Project: i.Project, Cycle: cycle, Labels: i.Labels}
}

// markdownFor renders an issue's Markdown, with relative links into the
// repository's default branch.
func markdownFor(repo, src string) template.HTML {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	return renderMarkdown(src, "", func(fp string, image bool) string {
		if image {
			return repoURL(repo, "raw", "", fp)
		}
		return repoURL(repo, "blob", "", fp)
	})
}

// issueDetail loads an issue for its page.
func (h uiHandler) issueDetail(r *http.Request, b *browser, p page, n int64) (*issueData, error) {
	svc := b.service()
	d, err := svc.Issue(r.Context(), p.Repo.Name, n)
	if err != nil {
		return nil, err
	}
	t, err := svc.IssueTracker(r.Context(), p.Repo.Name)
	if err != nil {
		return nil, err
	}
	level := access.ParseAccessLevel(p.Repo.Access)
	out := &issueData{
		Issue:       d,
		Description: markdownFor(p.Repo.Name, d.Description),
		CanWrite:    b.user != nil && level >= access.ReadWriteAccess,
		Labels:      t.Labels,
		Key:         t.Key,
		Statuses:    issue.Statuses,
		Priorities:  issue.PriorityNames,
		Props:       propsOf(d.Issue),
	}
	out.Projects, _ = svc.IssueProjects(r.Context(), p.Repo.Name)
	out.Cycles, _ = svc.IssueCycles(r.Context(), p.Repo.Name)
	for i := range d.Activity {
		e := &d.Activity[i]
		if e.Kind == issue.EventCreated {
			continue
		}
		out.Timeline = append(out.Timeline, timelineItem{At: e.CreatedAt, Event: e})
	}
	for i := range d.Comments {
		c := &d.Comments[i]
		mine := b.user != nil && c.Author == b.user.Username()
		out.Timeline = append(out.Timeline, timelineItem{
			At: c.CreatedAt, Comment: c, Body: markdownFor(p.Repo.Name, c.Body),
			CanEdit: out.CanWrite && (mine || level >= access.AdminAccess),
		})
	}
	sort.SliceStable(out.Timeline, func(i, j int) bool { return out.Timeline[i].At.Before(out.Timeline[j].At) })

	users := map[string]bool{}
	if b.user != nil {
		users[b.user.Username()] = true
	}
	if d.Assignee != "" {
		users[d.Assignee] = true
	}
	if d.Creator != "" {
		users[d.Creator] = true
	}
	if cs, err := b.collaborators(r.Context(), p.Repo.Name); err == nil {
		for _, c := range cs {
			if access.ParseAccessLevel(c.Access) >= access.ReadOnlyAccess {
				users[c.Username] = true
			}
		}
	}
	for u := range users {
		out.Users = append(out.Users, u)
	}
	sort.Strings(out.Users)
	return out, nil
}

// issuePage shows an issue, and saves its property form.
func (h uiHandler) issuePage(w http.ResponseWriter, r *http.Request, b *browser, p page, n int64) {
	if r.Method == http.MethodPost {
		h.issueSaveProps(w, r, b, p, n)
		return
	}
	d, err := h.issueDetail(r, b, p, n)
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	p.Title = d.Issue.ID + " " + d.Issue.Title
	p.Data = d
	h.render(w, r, "issue", p)
}

func (h uiHandler) issueSaveProps(w http.ResponseWriter, r *http.Request, b *browser, p page, n int64) {
	f, err := readForm(w, r)
	var props issueProps
	if err == nil {
		props = issueProps{
			Status: f.Get("status"), Priority: f.Get("priority"),
			Assignee: strings.TrimSpace(f.Get("assignee")), Parent: strings.TrimSpace(f.Get("parent")),
			Labels: f["labels"], NewLabel: strings.TrimSpace(f.Get("new_label")),
			Project: f.Get("project"), Cycle: f.Get("cycle"),
		}
		labels := slices.Clone(props.Labels)
		for _, l := range strings.Split(props.NewLabel, ",") {
			if l = strings.TrimSpace(l); l != "" {
				labels = append(labels, l)
			}
		}
		u := ops.IssueUpdate{
			Status: &props.Status, Priority: &props.Priority, Assignee: &props.Assignee,
			Parent: &props.Parent, Labels: &labels, Project: &props.Project, Cycle: &props.Cycle,
		}
		if _, err = b.service().UpdateIssue(r.Context(), p.Repo.Name, n, u); err == nil {
			http.Redirect(w, r, withDone(issueURL(p.Repo.Name, n), "issue-saved"), http.StatusSeeOther)
			return
		}
	}
	d, derr := h.issueDetail(r, b, p, n)
	if derr != nil {
		h.opsFail(w, r, b, p, derr)
		return
	}
	d.Props = props
	p.Title = d.Issue.ID + " " + d.Issue.Title
	p.Data = d
	h.renderForm(w, r, b, p, "issue", err, func(m string) { d.Err = m })
}

// --- New and edit ---

type issueFormData struct {
	Key, Title, Description string
	// Users are suggestions for the assignee field.
	Users                []string
	Projects             []ops.IssueProject
	Cycles               []ops.IssueCycle
	Props                issueProps
	Labels               []ops.IssueLabel
	Statuses, Priorities []string
	// Number is set when editing.
	Number int64
	ID     string
	Err    string
}

func (h uiHandler) issueNew(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	svc := b.service()
	p.Title = "New issue · " + p.Repo.Name
	t, err := svc.IssueTracker(r.Context(), p.Repo.Name)
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	d := &issueFormData{
		Key: t.Key, Labels: t.Labels, Statuses: issue.Statuses, Priorities: issue.PriorityNames,
		Props: issueProps{Status: issue.Todo, Priority: "none", Parent: r.URL.Query().Get("parent")},
	}
	if b.user != nil {
		d.Users = []string{b.user.Username()}
	}
	d.Projects, _ = svc.IssueProjects(r.Context(), p.Repo.Name)
	d.Cycles, _ = svc.IssueCycles(r.Context(), p.Repo.Name)
	d.Props.Project = r.URL.Query().Get("project")
	d.Props.Cycle = r.URL.Query().Get("cycle")
	p.Data = d
	if r.Method != http.MethodPost {
		if b.user == nil || access.ParseAccessLevel(p.Repo.Access) < access.ReadWriteAccess {
			h.opsFail(w, r, b, p, errForbiddenFor(b))
			return
		}
		if s := r.URL.Query().Get("status"); s != "" {
			if st, ok := issue.ParseStatus(s); ok {
				d.Props.Status = st
			}
		}
		h.render(w, r, "issuenew", p)
		return
	}
	f, err := readForm(w, r)
	if err == nil {
		d.Title, d.Description = f.Get("title"), f.Get("description")
		d.Props = issueProps{
			Status: f.Get("status"), Priority: f.Get("priority"), Assignee: strings.TrimSpace(f.Get("assignee")),
			Parent: strings.TrimSpace(f.Get("parent")), Labels: f["labels"], NewLabel: strings.TrimSpace(f.Get("new_label")),
			Project: f.Get("project"), Cycle: f.Get("cycle"),
		}
		labels := slices.Clone(d.Props.Labels)
		for _, l := range strings.Split(d.Props.NewLabel, ",") {
			if l = strings.TrimSpace(l); l != "" {
				labels = append(labels, l)
			}
		}
		var created ops.Issue
		created, err = svc.CreateIssue(r.Context(), p.Repo.Name, ops.IssueCreate{
			Title: d.Title, Description: d.Description, Status: d.Props.Status, Priority: d.Props.Priority,
			Assignee: d.Props.Assignee, Labels: labels, Parent: d.Props.Parent, Project: d.Props.Project, Cycle: d.Props.Cycle,
		})
		if err == nil {
			http.Redirect(w, r, withDone(issueURL(p.Repo.Name, created.Number), "issue-created"), http.StatusSeeOther)
			return
		}
	}
	h.renderForm(w, r, b, p, "issuenew", err, func(m string) { d.Err = m })
}

// errForbiddenFor is the refusal for a page that needs write access: sign
// in, or you can't.
func errForbiddenFor(b *browser) error {
	if b.user == nil {
		return ops.Invalid("sign in to do that")
	}
	return errForbidden
}

func (h uiHandler) issueEdit(w http.ResponseWriter, r *http.Request, b *browser, p page, n int64) {
	svc := b.service()
	i, err := svc.Issue(r.Context(), p.Repo.Name, n)
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	p.Title = "Edit " + i.ID
	d := &issueFormData{Number: n, ID: i.ID, Title: i.Title, Description: i.Description}
	p.Data = d
	if r.Method != http.MethodPost {
		if b.user == nil || access.ParseAccessLevel(p.Repo.Access) < access.ReadWriteAccess {
			h.opsFail(w, r, b, p, errForbiddenFor(b))
			return
		}
		h.render(w, r, "issueedit", p)
		return
	}
	f, err := readForm(w, r)
	if err == nil {
		d.Title, d.Description = f.Get("title"), f.Get("description")
		if _, err = svc.UpdateIssue(r.Context(), p.Repo.Name, n, ops.IssueUpdate{Title: &d.Title, Description: &d.Description}); err == nil {
			http.Redirect(w, r, withDone(issueURL(p.Repo.Name, n), "issue-saved"), http.StatusSeeOther)
			return
		}
	}
	h.renderForm(w, r, b, p, "issueedit", err, func(m string) { d.Err = m })
}

// --- Comments ---

func (h uiHandler) issueComment(w http.ResponseWriter, r *http.Request, b *browser, p page, n int64) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, issueURL(p.Repo.Name, n), http.StatusSeeOther)
		return
	}
	f, err := readForm(w, r)
	body := ""
	if err == nil {
		body = f.Get("body")
		var c ops.IssueComment
		if c, err = b.service().AddIssueComment(r.Context(), p.Repo.Name, n, body); err == nil {
			http.Redirect(w, r, withDone(issueURL(p.Repo.Name, n), "comment-added")+"#comment-"+strconv.FormatInt(c.ID, 10), http.StatusSeeOther)
			return
		}
	}
	d, derr := h.issueDetail(r, b, p, n)
	if derr != nil {
		h.opsFail(w, r, b, p, derr)
		return
	}
	d.CommentDraft = body
	p.Title = d.Issue.ID + " " + d.Issue.Title
	p.Data = d
	h.renderForm(w, r, b, p, "issue", err, func(m string) { d.CommentErr = m })
}

type commentEditData struct {
	ID      string
	Number  int64
	Comment ops.IssueComment
	Body    string
	Err     string
}

func (h uiHandler) issueCommentChange(w http.ResponseWriter, r *http.Request, b *browser, p page, n, id int64, op string) {
	svc := b.service()
	i, err := svc.Issue(r.Context(), p.Repo.Name, n)
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	var c *ops.IssueComment
	for k := range i.Comments {
		if i.Comments[k].ID == id {
			c = &i.Comments[k]
		}
	}
	if c == nil {
		h.fail(w, r, p, errNotFound)
		return
	}
	back := issueURL(p.Repo.Name, n)
	if op == "delete" {
		if r.Method == http.MethodPost {
			if _, err := readForm(w, r); err != nil {
				h.opsFail(w, r, b, p, err)
				return
			}
			if err := svc.DeleteIssueComment(r.Context(), p.Repo.Name, n, id); err != nil {
				h.opsFail(w, r, b, p, err)
				return
			}
			http.Redirect(w, r, withDone(back, "comment-deleted"), http.StatusSeeOther)
			return
		}
		p.Title = "Delete comment · " + i.ID
		p.Data = confirmData{
			Heading: "Delete this comment?",
			What:    excerpt(c.Body, 80),
			Message: "It's removed from " + i.ID + " for good.",
			Action:  issueURL(p.Repo.Name, n, "comments", strconv.FormatInt(id, 10), "delete"),
			Button:  "Delete comment",
			Cancel:  back + "#comment-" + strconv.FormatInt(id, 10),
		}
		h.render(w, r, "confirm", p)
		return
	}

	d := &commentEditData{ID: i.ID, Number: n, Comment: *c, Body: c.Body}
	p.Title = "Edit comment · " + i.ID
	p.Data = d
	if r.Method != http.MethodPost {
		h.render(w, r, "commentedit", p)
		return
	}
	f, err := readForm(w, r)
	if err == nil {
		d.Body = f.Get("body")
		if _, err = svc.UpdateIssueComment(r.Context(), p.Repo.Name, n, id, d.Body); err == nil {
			http.Redirect(w, r, withDone(back, "comment-saved")+"#comment-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
			return
		}
	}
	h.renderForm(w, r, b, p, "commentedit", err, func(m string) { d.Err = m })
}

// excerpt is the start of s on one line, cut at about n characters.
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (h uiHandler) issueDelete(w http.ResponseWriter, r *http.Request, b *browser, p page, n int64) {
	svc := b.service()
	i, err := svc.Issue(r.Context(), p.Repo.Name, n)
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	if r.Method == http.MethodPost {
		if _, err := readForm(w, r); err != nil {
			h.opsFail(w, r, b, p, err)
			return
		}
		if err := svc.DeleteIssue(r.Context(), p.Repo.Name, n); err != nil {
			h.opsFail(w, r, b, p, err)
			return
		}
		http.Redirect(w, r, withDone(repoURL(p.Repo.Name, "issues", "", ""), "issue-deleted"), http.StatusSeeOther)
		return
	}
	if b.user == nil || access.ParseAccessLevel(p.Repo.Access) < access.ReadWriteAccess {
		h.opsFail(w, r, b, p, errForbiddenFor(b))
		return
	}
	msg := "Its comments and activity go with it, for good. Canceling it instead keeps the record."
	if len(i.SubIssues) > 0 {
		msg = "Its comments and activity go with it, for good, and its " + plural(len(i.SubIssues), "sub-issue", "sub-issues") + " lose their parent. Canceling it instead keeps the record."
	}
	p.Title = "Delete " + i.ID
	p.Data = confirmData{
		Heading: "Delete this issue?",
		What:    i.ID + " " + i.Title,
		Message: msg,
		Action:  issueURL(p.Repo.Name, n, "delete"),
		Button:  "Delete issue",
		Cancel:  issueURL(p.Repo.Name, n),
	}
	h.render(w, r, "confirm", p)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// --- Settings: the key and labels ---

type issueSettingsData struct {
	Tracker  ops.IssueTracker
	IsAdmin  bool
	CanWrite bool
	Key      string
	KeyErr   string
	Label    ops.IssueLabel
	LabelErr string
}

func (h uiHandler) issueSettings(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	svc := b.service()
	p.Title = "Issue settings · " + p.Repo.Name
	level := access.ParseAccessLevel(p.Repo.Access)
	d := &issueSettingsData{IsAdmin: b.user != nil && level >= access.AdminAccess, CanWrite: b.user != nil && level >= access.ReadWriteAccess, Label: ops.IssueLabel{Color: "#8f897f"}}
	p.Data = d
	load := func() bool {
		t, err := svc.IssueTracker(r.Context(), p.Repo.Name)
		if err != nil {
			h.opsFail(w, r, b, p, err)
			return false
		}
		d.Tracker = t
		if d.Key == "" {
			d.Key = t.Key
		}
		return true
	}
	if r.Method != http.MethodPost {
		if load() {
			h.render(w, r, "issuesettings", p)
		}
		return
	}
	f, err := readForm(w, r)
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	back := repoURL(p.Repo.Name, "issues/settings", "", "")
	var setErr func(string)
	switch f.Get("op") {
	case "key":
		d.Key = strings.TrimSpace(f.Get("key"))
		if err = svc.SetIssueKey(r.Context(), p.Repo.Name, d.Key); err == nil {
			http.Redirect(w, r, withDone(back, "key-saved"), http.StatusSeeOther)
			return
		}
		setErr = func(m string) { d.KeyErr = m }
	case "label":
		d.Label = ops.IssueLabel{Name: f.Get("name"), Color: f.Get("color"), Description: f.Get("description")}
		if _, err = svc.CreateIssueLabel(r.Context(), p.Repo.Name, d.Label); err == nil {
			http.Redirect(w, r, withDone(back, "label-created"), http.StatusSeeOther)
			return
		}
		setErr = func(m string) { d.LabelErr = m }
	case "delete-label":
		if err = svc.DeleteIssueLabel(r.Context(), p.Repo.Name, f.Get("name")); err == nil {
			http.Redirect(w, r, withDone(back, "label-deleted"), http.StatusSeeOther)
			return
		}
		setErr = func(m string) { d.LabelErr = m }
	default:
		h.fail(w, r, p, errNotFound)
		return
	}
	if !load() {
		return
	}
	h.renderForm(w, r, b, p, "issuesettings", err, setErr)
}

// --- Template helpers ---

// eventText says what an activity entry did, after the name of who did it.
func eventText(e ops.IssueEvent) string {
	switch e.Kind {
	case issue.EventStatus:
		return "moved it from " + issue.StatusLabel(e.From) + " to " + issue.StatusLabel(e.To)
	case issue.EventPriority:
		n, _ := issue.ParsePriority(e.To)
		if n == issue.PriorityNone {
			return "removed the priority"
		}
		return "set the priority to " + issue.PriorityLabel(n)
	case issue.EventAssignee:
		switch {
		case e.To == "":
			return "unassigned " + e.From
		case e.To == e.Actor:
			return "took it"
		}
		return "assigned it to " + e.To
	case issue.EventTitle:
		return "renamed it from “" + e.From + "”"
	case issue.EventDescription:
		return "changed the description"
	case issue.EventLabelAdded:
		return "added the label " + e.To
	case issue.EventLabelRemove:
		return "removed the label " + e.From
	case issue.EventParent:
		if e.To == "" {
			return "made it a top-level issue, no longer part of " + e.From
		}
		return "made it part of " + e.To
	case issue.EventProject:
		if e.To == "" {
			return "took it out of project " + e.From
		}
		return "moved it into project " + e.To
	case issue.EventCycle:
		if e.To == "" {
			return "took it out of cycle " + e.From
		}
		return "moved it into cycle " + e.To
	case issue.EventBranch:
		return "started branch " + e.To + " in " + e.From
	case issue.EventCommit:
		return "mentioned it in a commit"
	}
	return e.Kind
}

// statusIcon draws a status as Linear does: a dashed ring for backlog, an
// empty ring for todo, a ring filling up as work goes on, a check for done
// and a cross for canceled. The shapes, not only their color, tell them
// apart.
func statusIcon(status string) template.HTML {
	const open = `<svg class="sicon s-%s" viewBox="0 0 14 14" width="14" height="14" aria-hidden="true">`
	var body string
	switch status {
	case issue.Backlog:
		body = `<circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="2.2 1.8"/>`
	case issue.Todo:
		body = `<circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" stroke-width="1.5"/>`
	case issue.InProgress:
		body = `<circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M7 3.5a3.5 3.5 0 0 1 0 7z" fill="currentColor"/>`
	case issue.InReview:
		body = `<circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M7 3.5a3.5 3.5 0 1 1-3.5 3.5H7z" fill="currentColor"/>`
	case issue.Done:
		body = `<circle cx="7" cy="7" r="6.25" fill="currentColor"/><path d="M4.4 7.2l1.8 1.8 3.5-3.7" fill="none" stroke="var(--surface)" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>`
	case issue.Canceled:
		body = `<circle cx="7" cy="7" r="6.25" fill="currentColor"/><path d="M5 5l4 4M9 5l-4 4" stroke="var(--surface)" stroke-width="1.5" stroke-linecap="round"/>`
	}
	return template.HTML(strings.Replace(open, "%s", template.HTMLEscapeString(status), 1) + body + `</svg>`) //nolint:gosec // fixed markup; status escaped
}

// priorityIcon draws a priority: three bars filled to its level, an
// exclamation box for urgent, and a dash for none.
func priorityIcon(name string) template.HTML {
	n, _ := issue.ParsePriority(name)
	var body string
	switch n {
	case issue.PriorityUrgent:
		body = `<rect x="1" y="1" width="12" height="12" rx="3" fill="currentColor"/><path d="M7 4v3.6" stroke="var(--surface)" stroke-width="1.6" stroke-linecap="round"/><circle cx="7" cy="10" r=".9" fill="var(--surface)"/>`
	case issue.PriorityNone:
		body = `<path d="M3 7h1.5M6.25 7h1.5M9.5 7H11" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>`
	default:
		lit := map[int]int{issue.PriorityHigh: 3, issue.PriorityMedium: 2, issue.PriorityLow: 1}[n]
		for i, x := range []string{"2", "6", "10"} {
			h := []string{"4", "7", "10"}[i]
			y := []string{"9", "6", "3"}[i]
			op := "1"
			if i >= lit {
				op = ".3"
			}
			body += `<rect x="` + x + `" y="` + y + `" width="2.5" height="` + h + `" rx=".8" fill="currentColor" opacity="` + op + `"/>`
		}
	}
	return template.HTML(`<svg class="picon p-` + issue.PriorityName(n) + `" viewBox="0 0 14 14" width="14" height="14" aria-hidden="true">` + body + `</svg>`) //nolint:gosec // fixed markup
}

// labelDot draws a label's color as a dot. The page's CSP allows no style
// attributes, but an SVG fill is a presentation attribute, which it
// doesn't govern. Only colors the service accepted (#rrggbb) are drawn;
// anything else is a plain dot.
func labelDot(c string) template.HTML {
	fill := "currentColor"
	if validHex(c) {
		fill = c
	}
	return template.HTML(`<svg class="ldot" viewBox="0 0 8 8" width="8" height="8" aria-hidden="true"><circle cx="4" cy="4" r="4" fill="` + fill + `"/></svg>`) //nolint:gosec // fill is a validated color
}

func validHex(c string) bool {
	if len(c) != 7 || c[0] != '#' {
		return false
	}
	for _, r := range c[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// labelColors finds each label's color by name.
func labelColors(ls []ops.IssueLabel) map[string]string {
	m := map[string]string{}
	for _, l := range ls {
		m[strings.ToLower(l.Name)] = l.Color
	}
	return m
}

// dotFor is the dot for a label named in an issue, colored as the
// repository's label of that name.
func dotFor(m map[string]string, name string) template.HTML {
	return labelDot(m[strings.ToLower(name)])
}
