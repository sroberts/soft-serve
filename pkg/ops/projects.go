package ops

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/charmbracelet/soft-serve/pkg/issue"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// Projects and cycles, under the same rules as issues: reading needs read
// access, and anything else read-write access and a signed-in user.

// IssueProgress is how far along a project's or cycle's issues are.
// Canceled issues are counted apart and left out of Total; Percent is
// Completed of Total.
type IssueProgress struct {
	Total     int `json:"total"`
	Started   int `json:"started"`
	Completed int `json:"completed"`
	Canceled  int `json:"canceled"`
	Percent   int `json:"percent"`
}

// IssueProject is a body of work in a repository.
type IssueProject struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Status      string        `json:"status"`
	Lead        string        `json:"lead,omitempty"`
	StartDate   string        `json:"start_date,omitempty"`
	TargetDate  string        `json:"target_date,omitempty"`
	Progress    IssueProgress `json:"progress"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// IssueProjectInput describes a project to create. Status defaults to
// planned; dates are written 2026-12-15.
type IssueProjectInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	Lead        string `json:"lead,omitempty"`
	StartDate   string `json:"start_date,omitempty"`
	TargetDate  string `json:"target_date,omitempty"`
}

// IssueProjectUpdate changes the fields of a project that are set; "" clears
// the lead and the dates.
type IssueProjectUpdate struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	Lead        *string `json:"lead,omitempty"`
	StartDate   *string `json:"start_date,omitempty"`
	TargetDate  *string `json:"target_date,omitempty"`
}

// IssueCycle is a numbered, time-boxed sprint. Current is set for the
// cycle today falls in.
type IssueCycle struct {
	Number    int64         `json:"number"`
	Name      string        `json:"name,omitempty"`
	StartDate string        `json:"start_date"`
	EndDate   string        `json:"end_date"`
	Current   bool          `json:"current"`
	Progress  IssueProgress `json:"progress"`
}

// IssueCycleInput describes a cycle to create. Without dates it starts the
// day after the last cycle ends (or today) and lasts two weeks.
type IssueCycleInput struct {
	Name      string `json:"name,omitempty"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

// IssueCycleUpdate changes the fields of a cycle that are set.
type IssueCycleUpdate struct {
	Name      *string `json:"name,omitempty"`
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
}

const maxProjectName = 80

// today is the day "current" cycles are worked out from, in UTC.
var today = func() string { return time.Now().UTC().Format(issue.DateLayout) }

func progressOut(p issue.Progress) IssueProgress {
	out := IssueProgress{Total: p.Total, Started: p.Started, Completed: p.Completed, Canceled: p.Canceled}
	if p.Total > 0 {
		out.Percent = p.Completed * 100 / p.Total
	}
	return out
}

func (l *Local) projectOut(ctx context.Context, h db.Handler, p issue.Project) (IssueProject, error) {
	prog, err := issue.ProgressOf(ctx, h, "project_id", p.ID)
	if err != nil {
		return IssueProject{}, err
	}
	return IssueProject{
		Name: p.Name, Description: p.Description, Status: p.Status, Lead: p.Lead.String,
		StartDate: p.StartDate, TargetDate: p.TargetDate, Progress: progressOut(prog),
		CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(),
	}, nil
}

func cycleOut(ctx context.Context, h db.Handler, c issue.Cycle) (IssueCycle, error) {
	prog, err := issue.ProgressOf(ctx, h, "cycle_id", c.ID)
	if err != nil {
		return IssueCycle{}, err
	}
	d := today()
	return IssueCycle{
		Number: c.Number, Name: c.Name, StartDate: c.StartDate, EndDate: c.EndDate,
		Current: c.StartDate <= d && d <= c.EndDate, Progress: progressOut(prog),
	}, nil
}

// --- Projects ---

// IssueProjects implements IssueService.
func (l *Local) IssueProjects(ctx context.Context, repo string) ([]IssueProject, error) {
	r, _, err := l.issueRepo(ctx, repo, access.ReadOnlyAccess)
	if err != nil {
		return nil, err
	}
	h := db.FromContext(ctx)
	ps, err := issue.Projects(ctx, h, r.ID())
	if err != nil {
		return nil, err
	}
	out := make([]IssueProject, 0, len(ps))
	for _, p := range ps {
		o, err := l.projectOut(ctx, h, p)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// IssueProject implements IssueService.
func (l *Local) IssueProject(ctx context.Context, repo, name string) (IssueProject, error) {
	r, _, err := l.issueRepo(ctx, repo, access.ReadOnlyAccess)
	if err != nil {
		return IssueProject{}, err
	}
	h := db.FromContext(ctx)
	p, err := issue.ProjectByName(ctx, h, r.ID(), name)
	if err != nil {
		return IssueProject{}, issueErr(err, "project "+strconv.Quote(name))
	}
	return l.projectOut(ctx, h, p)
}

// checkProject validates a project's fields in place.
func (l *Local) checkProject(ctx context.Context, r proto.Repository, p *issue.Project, lead *string) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || utf8.RuneCountInString(p.Name) > maxProjectName {
		return Invalid("a project's name is 1 to %d characters", maxProjectName)
	}
	var err error
	if p.Description, err = checkText("a description", p.Description); err != nil {
		return err
	}
	st, ok := issue.ParseProjectStatus(p.Status)
	if !ok {
		return Invalid("unknown project status %q: use one of %s", p.Status, strings.Join(issue.ProjectStatuses, ", "))
	}
	p.Status = st
	for _, d := range []*string{&p.StartDate, &p.TargetDate} {
		v, ok := issue.ParseDate(*d)
		if !ok {
			return Invalid("write dates as 2026-12-15, not %q", *d)
		}
		*d = v
	}
	if p.StartDate != "" && p.TargetDate != "" && p.TargetDate < p.StartDate {
		return Invalid("a project's target date can't be before its start")
	}
	if lead != nil {
		if p.LeadID, err = l.assignee(ctx, r, *lead); err != nil {
			return err
		}
	}
	return nil
}

// CreateIssueProject implements IssueService.
func (l *Local) CreateIssueProject(ctx context.Context, repo string, in IssueProjectInput) (IssueProject, error) {
	r, _, err := l.issueRepo(ctx, repo, access.ReadWriteAccess)
	if err != nil {
		return IssueProject{}, err
	}
	if in.Status == "" {
		in.Status = issue.ProjectPlanned
	}
	p := issue.Project{RepoID: r.ID(), Name: in.Name, Description: in.Description, Status: in.Status, StartDate: in.StartDate, TargetDate: in.TargetDate}
	if err := l.checkProject(ctx, r, &p, &in.Lead); err != nil {
		return IssueProject{}, err
	}
	h := db.FromContext(ctx)
	p, err = issue.CreateProject(ctx, h, p)
	if errors.Is(err, db.ErrDuplicateKey) {
		return IssueProject{}, Invalid("there's already a project named %q", in.Name)
	}
	if err != nil {
		return IssueProject{}, err
	}
	return l.projectOut(ctx, h, p)
}

// UpdateIssueProject implements IssueService.
func (l *Local) UpdateIssueProject(ctx context.Context, repo, name string, u IssueProjectUpdate) (IssueProject, error) {
	r, _, err := l.issueRepo(ctx, repo, access.ReadWriteAccess)
	if err != nil {
		return IssueProject{}, err
	}
	h := db.FromContext(ctx)
	p, err := issue.ProjectByName(ctx, h, r.ID(), name)
	if err != nil {
		return IssueProject{}, issueErr(err, "project "+strconv.Quote(name))
	}
	old := p.Name
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&p.Name, u.Name)
	set(&p.Description, u.Description)
	set(&p.Status, u.Status)
	set(&p.StartDate, u.StartDate)
	set(&p.TargetDate, u.TargetDate)
	if err := l.checkProject(ctx, r, &p, u.Lead); err != nil {
		return IssueProject{}, err
	}
	if !strings.EqualFold(old, p.Name) {
		if _, err := issue.ProjectByName(ctx, h, r.ID(), p.Name); err == nil {
			return IssueProject{}, Invalid("there's already a project named %q", p.Name)
		}
	}
	if err := issue.UpdateProject(ctx, h, p); err != nil {
		return IssueProject{}, err
	}
	p, err = issue.ProjectByID(ctx, h, p.ID)
	if err != nil {
		return IssueProject{}, err
	}
	return l.projectOut(ctx, h, p)
}

// DeleteIssueProject implements IssueService.
func (l *Local) DeleteIssueProject(ctx context.Context, repo, name string) error {
	r, _, err := l.issueRepo(ctx, repo, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	h := db.FromContext(ctx)
	p, err := issue.ProjectByName(ctx, h, r.ID(), name)
	if err != nil {
		return issueErr(err, "project "+strconv.Quote(name))
	}
	return h.TransactionContext(ctx, func(tx *db.Tx) error { return issue.DeleteProject(ctx, tx, p.ID) })
}

// --- Cycles ---

// IssueCycles implements IssueService.
func (l *Local) IssueCycles(ctx context.Context, repo string) ([]IssueCycle, error) {
	r, _, err := l.issueRepo(ctx, repo, access.ReadOnlyAccess)
	if err != nil {
		return nil, err
	}
	h := db.FromContext(ctx)
	cs, err := issue.Cycles(ctx, h, r.ID())
	if err != nil {
		return nil, err
	}
	out := make([]IssueCycle, 0, len(cs))
	for _, c := range cs {
		o, err := cycleOut(ctx, h, c)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// findCycle reads a cycle reference: a number, or current, next or
// previous.
func findCycle(ctx context.Context, h db.Handler, repoID int64, ref string) (issue.Cycle, error) {
	ref = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ref), "#"))
	var c issue.Cycle
	var err error
	switch ref {
	case "current":
		c, err = issue.CycleOn(ctx, h, repoID, today())
		if errors.Is(err, issue.ErrNotFound) {
			return c, NotFound("no cycle is running today")
		}
	case "next":
		c, err = issue.CycleAfter(ctx, h, repoID, today())
		if errors.Is(err, issue.ErrNotFound) {
			return c, NotFound("no cycle is planned after today")
		}
	case "previous", "last":
		c, err = issue.CycleBefore(ctx, h, repoID, today())
		if errors.Is(err, issue.ErrNotFound) {
			return c, NotFound("no cycle has ended yet")
		}
	default:
		n, ok := parseNumber(ref)
		if !ok {
			return c, Invalid("a cycle is a number, or current, next or previous; not %q", ref)
		}
		c, err = issue.CycleByNumber(ctx, h, repoID, n)
		err = issueErr(err, "cycle "+ref)
	}
	return c, err
}

// IssueCycle implements IssueService.
func (l *Local) IssueCycle(ctx context.Context, repo, ref string) (IssueCycle, error) {
	r, _, err := l.issueRepo(ctx, repo, access.ReadOnlyAccess)
	if err != nil {
		return IssueCycle{}, err
	}
	h := db.FromContext(ctx)
	c, err := findCycle(ctx, h, r.ID(), ref)
	if err != nil {
		return IssueCycle{}, err
	}
	return cycleOut(ctx, h, c)
}

// checkCycle validates a cycle's dates, which mustn't overlap another's.
func checkCycle(ctx context.Context, h db.Handler, c *issue.Cycle) error {
	c.Name = strings.TrimSpace(c.Name)
	if utf8.RuneCountInString(c.Name) > maxProjectName {
		return Invalid("a cycle's name can be up to %d characters", maxProjectName)
	}
	for _, d := range []*string{&c.StartDate, &c.EndDate} {
		v, ok := issue.ParseDate(*d)
		if !ok || v == "" {
			return Invalid("a cycle needs start and end dates, written 2026-12-15")
		}
		*d = v
	}
	if c.EndDate < c.StartDate {
		return Invalid("a cycle can't end before it starts")
	}
	if o, err := issue.OverlappingCycle(ctx, h, c.RepoID, c.StartDate, c.EndDate, c.ID); err == nil {
		return Invalid("those dates overlap cycle %d (%s to %s)", o.Number, o.StartDate, o.EndDate)
	} else if !errors.Is(err, issue.ErrNotFound) {
		return err
	}
	return nil
}

// CreateIssueCycle implements IssueService.
func (l *Local) CreateIssueCycle(ctx context.Context, repo string, in IssueCycleInput) (IssueCycle, error) {
	r, _, err := l.issueRepo(ctx, repo, access.ReadWriteAccess)
	if err != nil {
		return IssueCycle{}, err
	}
	h := db.FromContext(ctx)
	c := issue.Cycle{RepoID: r.ID(), Name: in.Name, StartDate: in.StartDate, EndDate: in.EndDate}
	if c.StartDate == "" {
		// The day after the last cycle ends, or today.
		start := today()
		if cs, err := issue.Cycles(ctx, h, r.ID()); err == nil && len(cs) > 0 {
			if last, err := time.Parse(issue.DateLayout, cs[0].EndDate); err == nil {
				if next := last.AddDate(0, 0, 1).Format(issue.DateLayout); next > start {
					start = next
				}
			}
		}
		c.StartDate = start
	}
	if c.EndDate == "" {
		if s, err := time.Parse(issue.DateLayout, c.StartDate); err == nil {
			c.EndDate = s.AddDate(0, 0, 13).Format(issue.DateLayout)
		}
	}
	if err := checkCycle(ctx, h, &c); err != nil {
		return IssueCycle{}, err
	}
	var created issue.Cycle
	err = h.TransactionContext(ctx, func(tx *db.Tx) error {
		var err error
		created, err = issue.CreateCycle(ctx, tx, c)
		return err
	})
	if err != nil {
		return IssueCycle{}, err
	}
	return cycleOut(ctx, h, created)
}

// UpdateIssueCycle implements IssueService.
func (l *Local) UpdateIssueCycle(ctx context.Context, repo, ref string, u IssueCycleUpdate) (IssueCycle, error) {
	r, _, err := l.issueRepo(ctx, repo, access.ReadWriteAccess)
	if err != nil {
		return IssueCycle{}, err
	}
	h := db.FromContext(ctx)
	c, err := findCycle(ctx, h, r.ID(), ref)
	if err != nil {
		return IssueCycle{}, err
	}
	if u.Name != nil {
		c.Name = *u.Name
	}
	if u.StartDate != nil {
		c.StartDate = *u.StartDate
	}
	if u.EndDate != nil {
		c.EndDate = *u.EndDate
	}
	if err := checkCycle(ctx, h, &c); err != nil {
		return IssueCycle{}, err
	}
	if err := issue.UpdateCycle(ctx, h, c); err != nil {
		return IssueCycle{}, err
	}
	return cycleOut(ctx, h, c)
}

// DeleteIssueCycle implements IssueService.
func (l *Local) DeleteIssueCycle(ctx context.Context, repo, ref string) error {
	r, _, err := l.issueRepo(ctx, repo, access.ReadWriteAccess)
	if err != nil {
		return err
	}
	h := db.FromContext(ctx)
	c, err := findCycle(ctx, h, r.ID(), ref)
	if err != nil {
		return err
	}
	return h.TransactionContext(ctx, func(tx *db.Tx) error { return issue.DeleteCycle(ctx, tx, c.ID) })
}

// projectFor finds a project for an issue by name; "" is none.
func projectFor(ctx context.Context, r proto.Repository, name string) (sql.NullInt64, string, error) {
	if strings.TrimSpace(name) == "" {
		return sql.NullInt64{}, "", nil
	}
	p, err := issue.ProjectByName(ctx, db.FromContext(ctx), r.ID(), name)
	if err != nil {
		return sql.NullInt64{}, "", issueErr(err, "project "+strconv.Quote(name))
	}
	return sql.NullInt64{Int64: p.ID, Valid: true}, p.Name, nil
}

// cycleFor finds a cycle for an issue; "" is none.
func cycleFor(ctx context.Context, r proto.Repository, ref string) (sql.NullInt64, int64, error) {
	if strings.TrimSpace(ref) == "" {
		return sql.NullInt64{}, 0, nil
	}
	c, err := findCycle(ctx, db.FromContext(ctx), r.ID(), ref)
	if err != nil {
		return sql.NullInt64{}, 0, err
	}
	return sql.NullInt64{Int64: c.ID, Valid: true}, c.Number, nil
}
