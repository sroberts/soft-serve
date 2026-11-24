package ops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/charmbracelet/soft-serve/pkg/issue"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// IssueService is issue tracking: the issues, labels and comments of the
// repositories an actor can read. Anyone who can read a repository can read
// its issues; creating, editing and commenting need read-write access, and
// changing the repository's issue key needs admin access.
type IssueService interface {
	// IssueTracker describes a repository's issue settings: its key, the
	// workflow's statuses, the priorities and the labels.
	IssueTracker(ctx context.Context, repo string) (IssueTracker, error)
	// SetIssueKey changes a repository's issue key. Existing issues are
	// renumbered under it: ICE-12 becomes NEW-12.
	SetIssueKey(ctx context.Context, repo, key string) error

	// ListIssues lists issues in one repository, or with no repository in
	// q, every repository the actor can read.
	ListIssues(ctx context.Context, q IssueQuery) (IssueList, error)
	// Issue is one issue in full: its sub-issues, comments, activity and
	// linked commits.
	Issue(ctx context.Context, repo string, number int64) (IssueDetail, error)
	// FindIssue finds the repository and number of an identifier such as
	// ICE-12.
	FindIssue(ctx context.Context, id string) (repo string, number int64, err error)
	// CreateIssue creates an issue. Labels that don't exist yet are
	// created.
	CreateIssue(ctx context.Context, repo string, c IssueCreate) (Issue, error)
	// UpdateIssue changes the fields set in u, and logs each change.
	UpdateIssue(ctx context.Context, repo string, number int64, u IssueUpdate) (Issue, error)
	// DeleteIssue deletes an issue for good. Its sub-issues stay, without
	// a parent. Setting the status to canceled is the gentler way.
	DeleteIssue(ctx context.Context, repo string, number int64) error

	// AddIssueComment comments on an issue.
	AddIssueComment(ctx context.Context, repo string, number int64, body string) (IssueComment, error)
	// UpdateIssueComment changes a comment. Only its author, or one of the
	// repository's admins, can.
	UpdateIssueComment(ctx context.Context, repo string, number, id int64, body string) (IssueComment, error)
	// DeleteIssueComment deletes a comment, under the same rule.
	DeleteIssueComment(ctx context.Context, repo string, number, id int64) error

	// IssueLabels lists a repository's labels.
	IssueLabels(ctx context.Context, repo string) ([]IssueLabel, error)
	// CreateIssueLabel adds a label.
	CreateIssueLabel(ctx context.Context, repo string, l IssueLabel) (IssueLabel, error)
	// UpdateIssueLabel changes the fields of a label set in u.
	UpdateIssueLabel(ctx context.Context, repo, name string, u IssueLabelUpdate) (IssueLabel, error)
	// DeleteIssueLabel deletes a label, taking it off every issue.
	DeleteIssueLabel(ctx context.Context, repo, name string) error

	// IssueProjects lists a repository's projects, with their progress.
	IssueProjects(ctx context.Context, repo string) ([]IssueProject, error)
	// IssueProject is one project, by name.
	IssueProject(ctx context.Context, repo, name string) (IssueProject, error)
	// CreateIssueProject adds a project.
	CreateIssueProject(ctx context.Context, repo string, p IssueProjectInput) (IssueProject, error)
	// UpdateIssueProject changes the fields of a project set in u.
	UpdateIssueProject(ctx context.Context, repo, name string, u IssueProjectUpdate) (IssueProject, error)
	// DeleteIssueProject deletes a project; its issues stay, in none.
	DeleteIssueProject(ctx context.Context, repo, name string) error

	// IssueCycles lists a repository's cycles, newest first.
	IssueCycles(ctx context.Context, repo string) ([]IssueCycle, error)
	// IssueCycle is one cycle: a number, or current, next or previous.
	IssueCycle(ctx context.Context, repo, ref string) (IssueCycle, error)
	// CreateIssueCycle adds the next cycle.
	CreateIssueCycle(ctx context.Context, repo string, c IssueCycleInput) (IssueCycle, error)
	// UpdateIssueCycle changes a cycle's name or dates.
	UpdateIssueCycle(ctx context.Context, repo, ref string, u IssueCycleUpdate) (IssueCycle, error)
	// DeleteIssueCycle deletes a cycle; its issues stay, in none.
	DeleteIssueCycle(ctx context.Context, repo, ref string) error
}

var _ IssueService = (*Local)(nil)

// Issue is an issue, as lists show it.
type Issue struct {
	// ID is the issue's identifier, such as ICE-12.
	ID       string `json:"id"`
	Repo     string `json:"repo"`
	Number   int64  `json:"number"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Category string `json:"status_category"`
	Priority string `json:"priority"`
	Assignee string `json:"assignee,omitempty"`
	Creator  string `json:"creator,omitempty"`
	// Labels are the issue's label names.
	Labels []string `json:"labels"`
	// Parent is the parent issue's identifier.
	Parent string `json:"parent,omitempty"`
	// Project is the project's name, and Cycle the cycle's number.
	Project      string     `json:"project,omitempty"`
	Cycle        int64      `json:"cycle,omitempty"`
	CommentCount int        `json:"comment_count"`
	Description  string     `json:"description,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	CanceledAt   *time.Time `json:"canceled_at,omitempty"`
}

// IssueDetail is an issue in full.
type IssueDetail struct {
	Issue
	SubIssues []Issue        `json:"sub_issues"`
	Comments  []IssueComment `json:"comments"`
	Activity  []IssueEvent   `json:"activity"`
	Commits   []IssueCommit  `json:"commits"`
}

// IssueComment is a comment on an issue.
type IssueComment struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author,omitempty"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Edited    bool      `json:"edited"`
}

// IssueEvent is a change in an issue's activity: Kind is created, title,
// description, status, priority, assignee, label_added, label_removed,
// parent, commit or branch, and From and To are the values before and
// after.
type IssueEvent struct {
	Actor     string    `json:"actor,omitempty"`
	Kind      string    `json:"kind"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// IssueCommit is a commit that mentions an issue. Closes is set when it
// says it fixes, closes or resolves it.
type IssueCommit struct {
	Repo      string    `json:"repo"`
	SHA       string    `json:"sha"`
	Summary   string    `json:"summary"`
	Closes    bool      `json:"closes"`
	CreatedAt time.Time `json:"linked_at"`
}

// IssueLabel is one of a repository's labels. Color is a hex color such
// as #e5484d, or empty.
type IssueLabel struct {
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
}

// IssueLabelUpdate changes the fields of a label that are set.
type IssueLabelUpdate struct {
	Name        *string `json:"name,omitempty"`
	Color       *string `json:"color,omitempty"`
	Description *string `json:"description,omitempty"`
}

// IssueStatus is a status on the workflow.
type IssueStatus struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Category string `json:"category"`
}

// IssueTracker is a repository's issue settings.
type IssueTracker struct {
	Repo       string        `json:"repo"`
	Key        string        `json:"key"`
	Statuses   []IssueStatus `json:"statuses"`
	Priorities []string      `json:"priorities"`
	Labels     []IssueLabel  `json:"labels"`
	// Open and Closed count the repository's issues.
	Open   int `json:"open"`
	Closed int `json:"closed"`
}

// IssueQuery selects issues.
type IssueQuery struct {
	// Repo is the repository; every readable one when empty.
	Repo string `json:"repo,omitempty"`
	// Status, if any, are the statuses to include. Without them, open
	// issues are listed, and closed ones too when Closed is set.
	Status []string `json:"status,omitempty"`
	Closed bool     `json:"closed,omitempty"`
	// Priority, if any, are the priorities to include.
	Priority []string `json:"priority,omitempty"`
	// Assignee is a username, "me" or "none".
	Assignee string `json:"assignee,omitempty"`
	// Label are label names an issue must all have.
	Label []string `json:"label,omitempty"`
	// Parent is an identifier whose sub-issues to list.
	Parent string `json:"parent,omitempty"`
	// Project is a project's name, or "none"; Cycle a cycle's number,
	// current, next, previous or "none". Both need Repo.
	Project string `json:"project,omitempty"`
	Cycle   string `json:"cycle,omitempty"`
	// Query matches the title, the description or the identifier.
	Query string `json:"q,omitempty"`
	// Sort is priority (the default), updated, created, number or manual.
	Sort   string `json:"sort,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
	// Description includes each issue's description.
	Description bool `json:"description,omitempty"`
}

// IssueList is a page of issues and how many there are in all.
type IssueList struct {
	Issues []Issue `json:"issues"`
	Total  int     `json:"total"`
}

// IssueCreate describes an issue to create.
type IssueCreate struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Status defaults to todo.
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
	// Assignee is a username, or "me".
	Assignee string   `json:"assignee,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	// Parent is the identifier or number of an issue in the same
	// repository.
	Parent string `json:"parent,omitempty"`
	// Project is a project's name; Cycle a cycle's number, current or
	// next.
	Project string `json:"project,omitempty"`
	Cycle   string `json:"cycle,omitempty"`
}

// IssueUpdate changes the fields of an issue that are set. Assignee and
// Parent can be set to "" to clear them. Labels replaces the labels;
// AddLabels and RemoveLabels change them one by one.
type IssueUpdate struct {
	Title        *string   `json:"title,omitempty"`
	Description  *string   `json:"description,omitempty"`
	Status       *string   `json:"status,omitempty"`
	Priority     *string   `json:"priority,omitempty"`
	Assignee     *string   `json:"assignee,omitempty"`
	Parent       *string   `json:"parent,omitempty"`
	Project      *string   `json:"project,omitempty"`
	Cycle        *string   `json:"cycle,omitempty"`
	Labels       *[]string `json:"labels,omitempty"`
	AddLabels    []string  `json:"add_labels,omitempty"`
	RemoveLabels []string  `json:"remove_labels,omitempty"`
}

// Limits on what an issue holds.
const (
	maxIssueTitle = 256
	maxIssueText  = 65536
	maxLabelName  = 50
)

var labelColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// actorID is the actor's user ID, for the activity log.
func (l *Local) actorID() sql.NullInt64 {
	if l.actor.User == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: l.actor.User.ID(), Valid: true}
}

// issueRepo is the gate for a repository's issues: reading needs read
// access, and anything else a signed-in actor with need.
func (l *Local) issueRepo(ctx context.Context, name string, need access.AccessLevel) (proto.Repository, access.AccessLevel, error) {
	r, level, err := l.repo(ctx, name, need)
	if err != nil {
		return nil, level, err
	}
	if need > access.ReadOnlyAccess && l.actor.User == nil {
		return nil, level, proto.ErrUnauthorized
	}
	return r, level, nil
}

// tracker is the repository's tracker, made the first time it's needed.
func tracker(ctx context.Context, r proto.Repository) (issue.Tracker, error) {
	return issue.TrackerFor(ctx, db.FromContext(ctx), r.ID(), r.Name())
}

// issueErr turns a missing issue, label or comment into the caller's
// mistake.
func issueErr(err error, what string) error {
	if errors.Is(err, issue.ErrNotFound) || errors.Is(err, db.ErrRecordNotFound) {
		return NotFound("%s not found", what)
	}
	return err
}

// NotFoundError is something the caller asked for by name that isn't there.
type NotFoundError struct{ msg string }

func (e *NotFoundError) Error() string { return e.msg }

// NotFound returns a NotFoundError.
func NotFound(format string, args ...any) error {
	return &NotFoundError{msg: fmt.Sprintf(format, args...)}
}

// IsNotFound reports whether err is a NotFoundError.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

func issueOut(r issue.Row, labels []issue.Label, comments int, withDescription bool) Issue {
	out := Issue{
		ID:           r.Identifier(),
		Repo:         r.RepoName,
		Number:       r.Number,
		Title:        r.Title,
		Status:       r.Status,
		Category:     issue.Category(r.Status),
		Priority:     issue.PriorityName(r.Priority),
		Assignee:     r.Assignee.String,
		Creator:      r.Creator.String,
		Labels:       make([]string, len(labels)),
		CommentCount: comments,
		CreatedAt:    r.CreatedAt.UTC(),
		UpdatedAt:    r.UpdatedAt.UTC(),
	}
	for i, l := range labels {
		out.Labels[i] = l.Name
	}
	if r.ParentNumber.Valid {
		out.Parent = issue.FormatID(r.Key, r.ParentNumber.Int64)
	}
	out.Project, out.Cycle = r.ProjectName.String, r.CycleNumber.Int64
	if withDescription {
		out.Description = r.Description
	}
	at := func(t sql.NullTime) *time.Time {
		if !t.Valid {
			return nil
		}
		u := t.Time.UTC()
		return &u
	}
	out.StartedAt, out.CompletedAt, out.CanceledAt = at(r.StartedAt), at(r.CompletedAt), at(r.CanceledAt)
	return out
}

// issuesOut fills in the labels and comment counts of a page of issues.
func issuesOut(ctx context.Context, h db.Handler, rows []issue.Row, withDescription bool) ([]Issue, error) {
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	labels, err := issue.LabelsOf(ctx, h, ids)
	if err != nil {
		return nil, err
	}
	counts, err := issue.CommentCounts(ctx, h, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Issue, len(rows))
	for i, r := range rows {
		out[i] = issueOut(r, labels[r.ID], counts[r.ID], withDescription)
	}
	return out, nil
}

// IssueTracker implements IssueService.
func (l *Local) IssueTracker(ctx context.Context, name string) (IssueTracker, error) {
	r, _, err := l.issueRepo(ctx, name, access.ReadOnlyAccess)
	if err != nil {
		return IssueTracker{}, err
	}
	h := db.FromContext(ctx)
	t, err := tracker(ctx, r)
	if err != nil {
		return IssueTracker{}, err
	}
	out := IssueTracker{Repo: r.Name(), Key: t.Key, Priorities: issue.PriorityNames}
	for _, s := range issue.Statuses {
		out.Statuses = append(out.Statuses, IssueStatus{Name: s, Label: issue.StatusLabel(s), Category: issue.Category(s)})
	}
	ls, err := issue.Labels(ctx, h, r.ID())
	if err != nil {
		return IssueTracker{}, err
	}
	out.Labels = labelsOut(ls)
	_, out.Open, err = issue.List(ctx, h, issue.Filter{RepoIDs: []int64{r.ID()}, Limit: 1})
	if err != nil {
		return IssueTracker{}, err
	}
	_, out.Closed, err = issue.List(ctx, h, issue.Filter{RepoIDs: []int64{r.ID()}, Statuses: []string{issue.Done, issue.Canceled}, Limit: 1})
	if err != nil {
		return IssueTracker{}, err
	}
	return out, nil
}

func labelsOut(ls []issue.Label) []IssueLabel {
	out := make([]IssueLabel, len(ls))
	for i, l := range ls {
		out[i] = IssueLabel{Name: l.Name, Color: l.Color, Description: l.Description}
	}
	return out
}

// SetIssueKey implements IssueService.
func (l *Local) SetIssueKey(ctx context.Context, name, key string) error {
	r, _, err := l.issueRepo(ctx, name, access.AdminAccess)
	if err != nil {
		return err
	}
	key = strings.ToUpper(strings.TrimSpace(key))
	if !issue.ValidKey(key) {
		return Invalid("an issue key is 2 to 10 letters and digits, starting with a letter, such as ICE")
	}
	if _, err := tracker(ctx, r); err != nil {
		return err
	}
	if err := issue.SetKey(ctx, db.FromContext(ctx), r.ID(), key); err != nil {
		if errors.Is(err, db.ErrDuplicateKey) {
			return Invalid("another repository uses the key %s", key)
		}
		return err
	}
	return nil
}

// readableRepoIDs is the IDs of every repository the actor can read.
func (l *Local) readableRepoIDs(ctx context.Context) ([]int64, error) {
	repos, err := l.be.Repositories(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range repos {
		if l.actor.Admin || l.be.AccessLevelForUser(ctx, r.Name(), l.actor.User) >= access.ReadOnlyAccess {
			ids = append(ids, r.ID())
		}
	}
	return ids, nil
}

// userID finds a user for an assignee: a username, or "me".
func (l *Local) userID(ctx context.Context, name string) (int64, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if strings.EqualFold(name, "me") {
		if l.actor.User == nil {
			return 0, Invalid("sign in to use \"me\"")
		}
		return l.actor.User.ID(), nil
	}
	u, err := l.be.User(ctx, name)
	if err != nil || u == nil {
		return 0, Invalid("no user is named %q", name)
	}
	return u.ID(), nil
}

// ListIssues implements IssueService.
func (l *Local) ListIssues(ctx context.Context, q IssueQuery) (IssueList, error) {
	ctx = l.withUser(ctx)
	h := db.FromContext(ctx)
	f := issue.Filter{Closed: q.Closed, Labels: q.Label, Query: q.Query, Sort: q.Sort, Limit: q.Limit, Offset: q.Offset}
	var repo proto.Repository
	if q.Repo != "" {
		r, _, err := l.issueRepo(ctx, q.Repo, access.ReadOnlyAccess)
		if err != nil {
			return IssueList{}, err
		}
		if _, err := tracker(ctx, r); err != nil {
			return IssueList{}, err
		}
		repo = r
		f.RepoIDs = []int64{r.ID()}
	} else {
		ids, err := l.readableRepoIDs(ctx)
		if err != nil {
			return IssueList{}, err
		}
		f.RepoIDs = ids
	}
	for _, s := range q.Status {
		st, ok := issue.ParseStatus(s)
		if !ok {
			return IssueList{}, Invalid("unknown status %q: use one of %s", s, strings.Join(issue.Statuses, ", "))
		}
		f.Statuses = append(f.Statuses, st)
	}
	for _, p := range q.Priority {
		n, ok := issue.ParsePriority(p)
		if !ok {
			return IssueList{}, Invalid("unknown priority %q: use one of %s", p, strings.Join(issue.PriorityNames, ", "))
		}
		f.Priorities = append(f.Priorities, n)
	}
	if q.Sort != "" && !slices.Contains(issue.Sorts, q.Sort) {
		return IssueList{}, Invalid("unknown sort %q: use one of %s", q.Sort, strings.Join(issue.Sorts, ", "))
	}
	switch a := strings.TrimSpace(q.Assignee); {
	case a == "":
	case strings.EqualFold(a, "none"):
		var zero int64
		f.AssigneeID = &zero
	default:
		id, err := l.userID(ctx, a)
		if err != nil {
			return IssueList{}, err
		}
		f.AssigneeID = &id
	}
	if (q.Project != "" || q.Cycle != "") && repo == nil {
		return IssueList{}, Invalid("filter by project or cycle within one repository")
	}
	if q.Project != "" {
		var id int64
		if !strings.EqualFold(q.Project, "none") {
			p, _, err := projectFor(ctx, repo, q.Project)
			if err != nil {
				return IssueList{}, err
			}
			id = p.Int64
		}
		f.ProjectID = &id
		if len(q.Status) == 0 {
			f.Closed = true
		}
	}
	if q.Cycle != "" {
		var id int64
		if !strings.EqualFold(q.Cycle, "none") {
			c, _, err := cycleFor(ctx, repo, q.Cycle)
			if err != nil {
				return IssueList{}, err
			}
			id = c.Int64
		}
		f.CycleID = &id
		if len(q.Status) == 0 {
			f.Closed = true
		}
	}
	if q.Parent != "" {
		p, err := l.parentRow(ctx, repo, q.Parent)
		if err != nil {
			return IssueList{}, err
		}
		f.ParentID = &p.ID
		if len(q.Status) == 0 {
			f.Closed = true
		}
	}
	rows, total, err := issue.List(ctx, h, f)
	if err != nil {
		return IssueList{}, err
	}
	out, err := issuesOut(ctx, h, rows, q.Description)
	if err != nil {
		return IssueList{}, err
	}
	return IssueList{Issues: out, Total: total}, nil
}

// parentRow finds a parent given as an identifier, or a number in repo.
func (l *Local) parentRow(ctx context.Context, repo proto.Repository, ref string) (issue.Row, error) {
	h := db.FromContext(ctx)
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	if repo != nil {
		if n, ok := parseNumber(ref); ok {
			r, err := issue.ByNumber(ctx, h, repo.ID(), n)
			return r, issueErr(err, "issue "+ref)
		}
	}
	name, n, err := l.FindIssue(ctx, ref)
	if err != nil {
		return issue.Row{}, err
	}
	r, _, err := l.issueRepo(ctx, name, access.ReadOnlyAccess)
	if err != nil {
		return issue.Row{}, err
	}
	row, err := issue.ByNumber(ctx, h, r.ID(), n)
	return row, issueErr(err, "issue "+ref)
}

func parseNumber(s string) (int64, bool) {
	var n int64
	if s == "" || len(s) > 12 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, n > 0
}

// FindIssue implements IssueService.
func (l *Local) FindIssue(ctx context.Context, id string) (string, int64, error) {
	ctx = l.withUser(ctx)
	key, n, ok := issue.ParseID(id)
	if !ok {
		return "", 0, Invalid("%q isn't an issue identifier like ICE-12", id)
	}
	h := db.FromContext(ctx)
	t, err := issue.TrackerByKey(ctx, h, key)
	if err != nil {
		return "", 0, issueErr(err, "issue "+id)
	}
	repos, err := l.be.Repositories(ctx)
	if err != nil {
		return "", 0, err
	}
	for _, r := range repos {
		if r.ID() != t.RepoID {
			continue
		}
		if _, _, err := l.issueRepo(ctx, r.Name(), access.ReadOnlyAccess); err != nil {
			return "", 0, NotFound("issue %s not found", strings.ToUpper(id))
		}
		return r.Name(), n, nil
	}
	return "", 0, NotFound("issue %s not found", strings.ToUpper(id))
}

// Issue implements IssueService.
func (l *Local) Issue(ctx context.Context, name string, number int64) (IssueDetail, error) {
	ctx = l.withUser(ctx)
	r, _, err := l.issueRepo(ctx, name, access.ReadOnlyAccess)
	if err != nil {
		return IssueDetail{}, err
	}
	if _, err := tracker(ctx, r); err != nil {
		return IssueDetail{}, err
	}
	h := db.FromContext(ctx)
	row, err := issue.ByNumber(ctx, h, r.ID(), number)
	if err != nil {
		return IssueDetail{}, issueErr(err, "issue")
	}
	return l.detail(ctx, h, row)
}

func (l *Local) detail(ctx context.Context, h db.Handler, row issue.Row) (IssueDetail, error) {
	one, err := issuesOut(ctx, h, []issue.Row{row}, true)
	if err != nil {
		return IssueDetail{}, err
	}
	d := IssueDetail{Issue: one[0], SubIssues: []Issue{}, Comments: []IssueComment{}, Activity: []IssueEvent{}, Commits: []IssueCommit{}}
	kids, err := issue.Children(ctx, h, row.ID)
	if err != nil {
		return IssueDetail{}, err
	}
	if d.SubIssues, err = issuesOut(ctx, h, kids, false); err != nil {
		return IssueDetail{}, err
	}
	cs, err := issue.Comments(ctx, h, row.ID)
	if err != nil {
		return IssueDetail{}, err
	}
	for _, c := range cs {
		d.Comments = append(d.Comments, commentOut(c))
	}
	es, err := issue.Events(ctx, h, row.ID)
	if err != nil {
		return IssueDetail{}, err
	}
	for _, e := range es {
		d.Activity = append(d.Activity, IssueEvent{Actor: e.Actor.String, Kind: e.Kind, From: e.From, To: e.To, CreatedAt: e.CreatedAt.UTC()})
	}
	commits, err := issue.Commits(ctx, h, row.ID)
	if err != nil {
		return IssueDetail{}, err
	}
	for _, c := range commits {
		// A commit in a repository the actor can't read stays hidden.
		if c.RepoID != row.RepoID {
			if _, _, err := l.issueRepo(ctx, c.RepoName, access.ReadOnlyAccess); err != nil {
				continue
			}
		}
		d.Commits = append(d.Commits, IssueCommit{Repo: c.RepoName, SHA: c.SHA, Summary: c.Summary, Closes: c.Closes, CreatedAt: c.CreatedAt.UTC()})
	}
	return d, nil
}

func commentOut(c issue.Comment) IssueComment {
	return IssueComment{
		ID: c.ID, Author: c.Author.String, Body: c.Body,
		CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC(),
		Edited: c.UpdatedAt.After(c.CreatedAt),
	}
}

func checkTitle(title string) (string, error) {
	title = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(title, "\r", " "), "\n", " "))
	switch {
	case title == "":
		return "", Invalid("give the issue a title")
	case utf8.RuneCountInString(title) > maxIssueTitle:
		return "", Invalid("a title can be up to %d characters", maxIssueTitle)
	}
	return title, nil
}

func checkText(what, s string) (string, error) {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), " \n\t")
	if len(s) > maxIssueText {
		return "", Invalid("%s can be up to %d KiB", what, maxIssueText/1024)
	}
	return s, nil
}

// assignee checks a username for an assignee, who must be able to read
// the repository.
func (l *Local) assignee(ctx context.Context, r proto.Repository, name string) (sql.NullInt64, error) {
	if strings.TrimSpace(name) == "" {
		return sql.NullInt64{}, nil
	}
	id, err := l.userID(ctx, name)
	if err != nil {
		return sql.NullInt64{}, err
	}
	u, err := l.be.UserByID(ctx, id)
	if err != nil {
		return sql.NullInt64{}, err
	}
	if !u.IsAdmin() && l.be.AccessLevelForUser(ctx, r.Name(), u) < access.ReadOnlyAccess {
		return sql.NullInt64{}, Invalid("%s can't read %s, so they can't be assigned its issues", u.Username(), r.Name())
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

// parentFor checks a parent for an issue: it must be in the same
// repository, and not the issue or one of its own sub-issues.
func (l *Local) parentFor(ctx context.Context, r proto.Repository, ref string, self int64) (sql.NullInt64, int64, error) {
	if strings.TrimSpace(ref) == "" {
		return sql.NullInt64{}, 0, nil
	}
	p, err := l.parentRow(ctx, r, ref)
	if err != nil {
		return sql.NullInt64{}, 0, err
	}
	if p.RepoID != r.ID() {
		return sql.NullInt64{}, 0, Invalid("a parent has to be in the same repository")
	}
	h := db.FromContext(ctx)
	for cur, depth := p, 0; ; depth++ {
		if cur.ID == self {
			return sql.NullInt64{}, 0, Invalid("an issue can't be its own parent, or the parent of one of its parents")
		}
		if !cur.ParentID.Valid || depth > 100 {
			break
		}
		next, err := issue.ByID(ctx, h, cur.ParentID.Int64)
		if err != nil {
			break
		}
		cur = next
	}
	return sql.NullInt64{Int64: p.ID, Valid: true}, p.Number, nil
}

// label finds a label by name, creating it when create is set.
func label(ctx context.Context, h db.Handler, repoID int64, name string, create bool) (issue.Label, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxLabelName {
		return issue.Label{}, Invalid("a label's name is 1 to %d characters", maxLabelName)
	}
	lb, err := issue.LabelByName(ctx, h, repoID, name)
	if err == nil || !create || !errors.Is(err, issue.ErrNotFound) {
		return lb, issueErr(err, "label "+name)
	}
	return issue.CreateLabel(ctx, h, issue.Label{RepoID: repoID, Name: name})
}

// CreateIssue implements IssueService.
func (l *Local) CreateIssue(ctx context.Context, name string, c IssueCreate) (Issue, error) {
	ctx = l.withUser(ctx)
	r, _, err := l.issueRepo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return Issue{}, err
	}
	n := issue.New{Status: issue.Todo, CreatorID: l.actorID()}
	if n.Title, err = checkTitle(c.Title); err != nil {
		return Issue{}, err
	}
	if n.Description, err = checkText("a description", c.Description); err != nil {
		return Issue{}, err
	}
	if c.Status != "" {
		st, ok := issue.ParseStatus(c.Status)
		if !ok {
			return Issue{}, Invalid("unknown status %q: use one of %s", c.Status, strings.Join(issue.Statuses, ", "))
		}
		n.Status = st
	}
	if c.Priority != "" {
		p, ok := issue.ParsePriority(c.Priority)
		if !ok {
			return Issue{}, Invalid("unknown priority %q: use one of %s", c.Priority, strings.Join(issue.PriorityNames, ", "))
		}
		n.Priority = p
	}
	if n.AssigneeID, err = l.assignee(ctx, r, c.Assignee); err != nil {
		return Issue{}, err
	}
	if _, err := tracker(ctx, r); err != nil {
		return Issue{}, err
	}
	if n.ParentID, _, err = l.parentFor(ctx, r, c.Parent, 0); err != nil {
		return Issue{}, err
	}
	if n.ProjectID, _, err = projectFor(ctx, r, c.Project); err != nil {
		return Issue{}, err
	}
	if n.CycleID, _, err = cycleFor(ctx, r, c.Cycle); err != nil {
		return Issue{}, err
	}

	var row issue.Row
	err = db.FromContext(ctx).TransactionContext(ctx, func(tx *db.Tx) error {
		var err error
		if row, err = issue.Create(ctx, tx, r.ID(), n); err != nil {
			return err
		}
		if err := issue.Log(ctx, tx, row.ID, l.actorID(), issue.EventCreated, "", ""); err != nil {
			return err
		}
		for _, name := range c.Labels {
			lb, err := label(ctx, tx, r.ID(), name, true)
			if err != nil {
				return err
			}
			if _, err := issue.AddLabel(ctx, tx, row.ID, lb.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Issue{}, err
	}
	h := db.FromContext(ctx)
	out, err := issuesOut(ctx, h, []issue.Row{row}, true)
	if err != nil {
		return Issue{}, err
	}
	return out[0], nil
}

// UpdateIssue implements IssueService.
func (l *Local) UpdateIssue(ctx context.Context, name string, number int64, u IssueUpdate) (Issue, error) {
	ctx = l.withUser(ctx)
	r, _, err := l.issueRepo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return Issue{}, err
	}
	if _, err := tracker(ctx, r); err != nil {
		return Issue{}, err
	}
	h := db.FromContext(ctx)
	row, err := issue.ByNumber(ctx, h, r.ID(), number)
	if err != nil {
		return Issue{}, issueErr(err, "issue")
	}
	before := row
	type change struct{ kind, from, to string }
	var changes []change

	if u.Title != nil {
		t, err := checkTitle(*u.Title)
		if err != nil {
			return Issue{}, err
		}
		if t != row.Title {
			changes = append(changes, change{issue.EventTitle, row.Title, t})
			row.Title = t
		}
	}
	if u.Description != nil {
		d, err := checkText("a description", *u.Description)
		if err != nil {
			return Issue{}, err
		}
		if d != row.Description {
			changes = append(changes, change{issue.EventDescription, "", ""})
			row.Description = d
		}
	}
	if u.Status != nil {
		st, ok := issue.ParseStatus(*u.Status)
		if !ok {
			return Issue{}, Invalid("unknown status %q: use one of %s", *u.Status, strings.Join(issue.Statuses, ", "))
		}
		if st != row.Status {
			changes = append(changes, change{issue.EventStatus, row.Status, st})
			row.Status = st
		}
	}
	if u.Priority != nil {
		p, ok := issue.ParsePriority(*u.Priority)
		if !ok {
			return Issue{}, Invalid("unknown priority %q: use one of %s", *u.Priority, strings.Join(issue.PriorityNames, ", "))
		}
		if p != row.Priority {
			changes = append(changes, change{issue.EventPriority, issue.PriorityName(row.Priority), issue.PriorityName(p)})
			row.Priority = p
		}
	}
	if u.Assignee != nil {
		a, err := l.assignee(ctx, r, *u.Assignee)
		if err != nil {
			return Issue{}, err
		}
		if a != row.AssigneeID {
			to := ""
			if a.Valid {
				if au, err := l.be.UserByID(ctx, a.Int64); err == nil {
					to = au.Username()
				}
			}
			changes = append(changes, change{issue.EventAssignee, row.Assignee.String, to})
			row.AssigneeID = a
		}
	}
	if u.Parent != nil {
		p, pnum, err := l.parentFor(ctx, r, *u.Parent, row.ID)
		if err != nil {
			return Issue{}, err
		}
		if p != row.ParentID {
			from, to := "", ""
			if row.ParentNumber.Valid {
				from = issue.FormatID(row.Key, row.ParentNumber.Int64)
			}
			if p.Valid {
				to = issue.FormatID(row.Key, pnum)
			}
			changes = append(changes, change{issue.EventParent, from, to})
			row.ParentID = p
		}
	}

	if u.Project != nil {
		p, name, err := projectFor(ctx, r, *u.Project)
		if err != nil {
			return Issue{}, err
		}
		if p != row.ProjectID {
			changes = append(changes, change{issue.EventProject, row.ProjectName.String, name})
			row.ProjectID = p
		}
	}
	if u.Cycle != nil {
		c, num, err := cycleFor(ctx, r, *u.Cycle)
		if err != nil {
			return Issue{}, err
		}
		if c != row.CycleID {
			from, to := "", ""
			if row.CycleNumber.Valid {
				from = strconv.FormatInt(row.CycleNumber.Int64, 10)
			}
			if c.Valid {
				to = strconv.FormatInt(num, 10)
			}
			changes = append(changes, change{issue.EventCycle, from, to})
			row.CycleID = c
		}
	}

	err = h.TransactionContext(ctx, func(tx *db.Tx) error {
		touched := len(changes) > 0
		if touched {
			if err := issue.Update(ctx, tx, row, row.Status != before.Status); err != nil {
				return err
			}
		}
		for _, c := range changes {
			if err := issue.Log(ctx, tx, row.ID, l.actorID(), c.kind, c.from, c.to); err != nil {
				return err
			}
		}
		add, remove := u.AddLabels, u.RemoveLabels
		if u.Labels != nil {
			have, err := issue.LabelsOf(ctx, tx, []int64{row.ID})
			if err != nil {
				return err
			}
			want := map[string]bool{}
			for _, n := range *u.Labels {
				want[strings.ToLower(strings.TrimSpace(n))] = true
			}
			for _, lb := range have[row.ID] {
				if !want[strings.ToLower(lb.Name)] {
					remove = append(remove, lb.Name)
				}
			}
			add = append(add, *u.Labels...)
		}
		for _, n := range add {
			lb, err := label(ctx, tx, r.ID(), n, true)
			if err != nil {
				return err
			}
			added, err := issue.AddLabel(ctx, tx, row.ID, lb.ID)
			if err != nil {
				return err
			}
			if added {
				touched = true
				if err := issue.Log(ctx, tx, row.ID, l.actorID(), issue.EventLabelAdded, "", lb.Name); err != nil {
					return err
				}
			}
		}
		for _, n := range remove {
			lb, err := label(ctx, tx, r.ID(), n, false)
			if IsNotFound(err) {
				continue
			}
			if err != nil {
				return err
			}
			removed, err := issue.RemoveLabel(ctx, tx, row.ID, lb.ID)
			if err != nil {
				return err
			}
			if removed {
				touched = true
				if err := issue.Log(ctx, tx, row.ID, l.actorID(), issue.EventLabelRemove, lb.Name, ""); err != nil {
					return err
				}
			}
		}
		if touched && len(changes) == 0 {
			return issue.Touch(ctx, tx, row.ID)
		}
		return nil
	})
	if err != nil {
		return Issue{}, err
	}
	row, err = issue.ByID(ctx, h, row.ID)
	if err != nil {
		return Issue{}, err
	}
	out, err := issuesOut(ctx, h, []issue.Row{row}, true)
	if err != nil {
		return Issue{}, err
	}
	return out[0], nil
}

// DeleteIssue implements IssueService.
func (l *Local) DeleteIssue(ctx context.Context, name string, number int64) error {
	ctx = l.withUser(ctx)
	r, _, err := l.issueRepo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	if _, err := tracker(ctx, r); err != nil {
		return err
	}
	h := db.FromContext(ctx)
	row, err := issue.ByNumber(ctx, h, r.ID(), number)
	if err != nil {
		return issueErr(err, "issue")
	}
	return issue.Delete(ctx, h, row.ID)
}

// issueForWrite is the issue a comment goes on.
func (l *Local) issueForWrite(ctx context.Context, name string, number int64) (issue.Row, access.AccessLevel, error) {
	r, level, err := l.issueRepo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return issue.Row{}, level, err
	}
	if _, err := tracker(ctx, r); err != nil {
		return issue.Row{}, level, err
	}
	row, err := issue.ByNumber(ctx, db.FromContext(ctx), r.ID(), number)
	return row, level, issueErr(err, "issue")
}

// AddIssueComment implements IssueService.
func (l *Local) AddIssueComment(ctx context.Context, name string, number int64, body string) (IssueComment, error) {
	ctx = l.withUser(ctx)
	row, _, err := l.issueForWrite(ctx, name, number)
	if err != nil {
		return IssueComment{}, err
	}
	body, err = checkText("a comment", body)
	if err != nil {
		return IssueComment{}, err
	}
	if strings.TrimSpace(body) == "" {
		return IssueComment{}, Invalid("write something to comment")
	}
	var c issue.Comment
	err = db.FromContext(ctx).TransactionContext(ctx, func(tx *db.Tx) error {
		var err error
		if c, err = issue.AddComment(ctx, tx, row.ID, l.actorID(), body); err != nil {
			return err
		}
		return issue.Touch(ctx, tx, row.ID)
	})
	if err != nil {
		return IssueComment{}, err
	}
	return commentOut(c), nil
}

// ownComment is a comment the actor may change: their own, or any for one
// of the repository's admins.
func (l *Local) ownComment(ctx context.Context, name string, number, id int64) (issue.Comment, error) {
	row, level, err := l.issueForWrite(ctx, name, number)
	if err != nil {
		return issue.Comment{}, err
	}
	c, err := issue.CommentByID(ctx, db.FromContext(ctx), row.ID, id)
	if err != nil {
		return issue.Comment{}, issueErr(err, "comment")
	}
	mine := l.actor.User != nil && c.AuthorID.Valid && c.AuthorID.Int64 == l.actor.User.ID()
	if !mine && level < access.AdminAccess {
		return issue.Comment{}, Invalid("only its author or a repository admin can change a comment")
	}
	return c, nil
}

// UpdateIssueComment implements IssueService.
func (l *Local) UpdateIssueComment(ctx context.Context, name string, number, id int64, body string) (IssueComment, error) {
	ctx = l.withUser(ctx)
	c, err := l.ownComment(ctx, name, number, id)
	if err != nil {
		return IssueComment{}, err
	}
	body, err = checkText("a comment", body)
	if err != nil {
		return IssueComment{}, err
	}
	if strings.TrimSpace(body) == "" {
		return IssueComment{}, Invalid("a comment can't be empty; delete it instead")
	}
	h := db.FromContext(ctx)
	if err := issue.UpdateComment(ctx, h, c.ID, body); err != nil {
		return IssueComment{}, err
	}
	c, err = issue.CommentByID(ctx, h, c.IssueID, c.ID)
	if err != nil {
		return IssueComment{}, err
	}
	return commentOut(c), nil
}

// DeleteIssueComment implements IssueService.
func (l *Local) DeleteIssueComment(ctx context.Context, name string, number, id int64) error {
	ctx = l.withUser(ctx)
	c, err := l.ownComment(ctx, name, number, id)
	if err != nil {
		return err
	}
	return issue.DeleteComment(ctx, db.FromContext(ctx), c.ID)
}

// IssueLabels implements IssueService.
func (l *Local) IssueLabels(ctx context.Context, name string) ([]IssueLabel, error) {
	r, _, err := l.issueRepo(ctx, name, access.ReadOnlyAccess)
	if err != nil {
		return nil, err
	}
	ls, err := issue.Labels(ctx, db.FromContext(ctx), r.ID())
	if err != nil {
		return nil, err
	}
	return labelsOut(ls), nil
}

func checkLabel(lb *issue.Label) error {
	lb.Name = strings.TrimSpace(lb.Name)
	lb.Color = strings.ToLower(strings.TrimSpace(lb.Color))
	lb.Description = strings.TrimSpace(lb.Description)
	switch {
	case lb.Name == "" || utf8.RuneCountInString(lb.Name) > maxLabelName:
		return Invalid("a label's name is 1 to %d characters", maxLabelName)
	case lb.Color != "" && !labelColor.MatchString(lb.Color):
		return Invalid("a label's color is a hex color such as #e5484d")
	case utf8.RuneCountInString(lb.Description) > maxIssueTitle:
		return Invalid("a label's description can be up to %d characters", maxIssueTitle)
	}
	return nil
}

// CreateIssueLabel implements IssueService.
func (l *Local) CreateIssueLabel(ctx context.Context, name string, in IssueLabel) (IssueLabel, error) {
	r, _, err := l.issueRepo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return IssueLabel{}, err
	}
	lb := issue.Label{RepoID: r.ID(), Name: in.Name, Color: in.Color, Description: in.Description}
	if err := checkLabel(&lb); err != nil {
		return IssueLabel{}, err
	}
	lb, err = issue.CreateLabel(ctx, db.FromContext(ctx), lb)
	if errors.Is(err, db.ErrDuplicateKey) {
		return IssueLabel{}, Invalid("there's already a label named %q", lb.Name)
	}
	if err != nil {
		return IssueLabel{}, err
	}
	return IssueLabel{Name: lb.Name, Color: lb.Color, Description: lb.Description}, nil
}

// UpdateIssueLabel implements IssueService.
func (l *Local) UpdateIssueLabel(ctx context.Context, name, labelName string, u IssueLabelUpdate) (IssueLabel, error) {
	r, _, err := l.issueRepo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return IssueLabel{}, err
	}
	h := db.FromContext(ctx)
	lb, err := issue.LabelByName(ctx, h, r.ID(), labelName)
	if err != nil {
		return IssueLabel{}, issueErr(err, "label "+labelName)
	}
	old := lb.Name
	if u.Name != nil {
		lb.Name = *u.Name
	}
	if u.Color != nil {
		lb.Color = *u.Color
	}
	if u.Description != nil {
		lb.Description = *u.Description
	}
	if err := checkLabel(&lb); err != nil {
		return IssueLabel{}, err
	}
	if !strings.EqualFold(old, lb.Name) {
		if _, err := issue.LabelByName(ctx, h, r.ID(), lb.Name); err == nil {
			return IssueLabel{}, Invalid("there's already a label named %q", lb.Name)
		}
	}
	if err := issue.UpdateLabel(ctx, h, lb); err != nil {
		return IssueLabel{}, err
	}
	return IssueLabel{Name: lb.Name, Color: lb.Color, Description: lb.Description}, nil
}

// DeleteIssueLabel implements IssueService.
func (l *Local) DeleteIssueLabel(ctx context.Context, name, labelName string) error {
	r, _, err := l.issueRepo(ctx, name, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	h := db.FromContext(ctx)
	lb, err := issue.LabelByName(ctx, h, r.ID(), labelName)
	if err != nil {
		return issueErr(err, "label "+labelName)
	}
	return issue.DeleteLabel(ctx, h, lb.ID)
}
