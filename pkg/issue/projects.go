package issue

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/charmbracelet/soft-serve/pkg/db"
)

// Project statuses, as Linear's.
const (
	ProjectPlanned   = "planned"
	ProjectStarted   = "started"
	ProjectPaused    = "paused"
	ProjectCompleted = "completed"
	ProjectCanceled  = "canceled"
)

// ProjectStatuses are the statuses a project can have, in order.
var ProjectStatuses = []string{ProjectPlanned, ProjectStarted, ProjectPaused, ProjectCompleted, ProjectCanceled}

// ProjectStatusLabel names a project status in words.
func ProjectStatusLabel(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ParseProjectStatus reads a project status, ignoring case.
func ParseProjectStatus(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "cancelled" {
		s = ProjectCanceled
	}
	for _, st := range ProjectStatuses {
		if st == s {
			return st, true
		}
	}
	return "", false
}

// DateLayout is how project and cycle dates are written and stored.
const DateLayout = "2006-01-02"

// ParseDate reads a date such as 2026-12-15; empty is no date.
func ParseDate(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return "", false
	}
	return t.Format(DateLayout), true
}

// Project is a body of work in a repository.
type Project struct {
	ID          int64          `db:"id"`
	RepoID      int64          `db:"repo_id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Status      string         `db:"status"`
	LeadID      sql.NullInt64  `db:"lead_id"`
	Lead        sql.NullString `db:"lead"`
	StartDate   string         `db:"start_date"`
	TargetDate  string         `db:"target_date"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

// Cycle is a numbered, time-boxed sprint in a repository.
type Cycle struct {
	ID        int64     `db:"id"`
	RepoID    int64     `db:"repo_id"`
	Number    int64     `db:"number"`
	Name      string    `db:"name"`
	StartDate string    `db:"start_date"`
	EndDate   string    `db:"end_date"`
	CreatedAt time.Time `db:"created_at"`
}

// Progress counts the issues in a project or cycle by how far along they
// are. Canceled issues are counted apart, and left out of the total.
type Progress struct {
	Total     int `db:"total"`
	Started   int `db:"started"`
	Completed int `db:"completed"`
	Canceled  int `db:"canceled"`
}

const selectProject = `SELECT p.id, p.repo_id, p.name, p.description, p.status, p.lead_id, u.username AS lead,
	p.start_date, p.target_date, p.created_at, p.updated_at
FROM issue_projects p LEFT JOIN users u ON u.id = p.lead_id`

// Projects returns a repository's projects: active ones first, then by
// target date and name.
func Projects(ctx context.Context, h db.Handler, repoID int64) ([]Project, error) {
	var ps []Project
	err := h.SelectContext(ctx, &ps, h.Rebind(selectProject+` WHERE p.repo_id = ?
		ORDER BY CASE p.status WHEN 'started' THEN 0 WHEN 'planned' THEN 1 WHEN 'paused' THEN 2 ELSE 3 END,
		CASE WHEN p.target_date = '' THEN 1 ELSE 0 END, p.target_date, LOWER(p.name)`), repoID)
	return ps, wrap(err)
}

// ProjectByName returns one of a repository's projects, ignoring case.
func ProjectByName(ctx context.Context, h db.Handler, repoID int64, name string) (Project, error) {
	var p Project
	err := h.GetContext(ctx, &p, h.Rebind(selectProject+` WHERE p.repo_id = ? AND LOWER(p.name) = ?`), repoID, strings.ToLower(strings.TrimSpace(name)))
	return p, wrap(err)
}

// ProjectByID returns a project.
func ProjectByID(ctx context.Context, h db.Handler, id int64) (Project, error) {
	var p Project
	err := h.GetContext(ctx, &p, h.Rebind(selectProject+` WHERE p.id = ?`), id)
	return p, wrap(err)
}

// CreateProject adds a project, and fails with db.ErrDuplicateKey if the
// repository has one by that name.
func CreateProject(ctx context.Context, h db.Handler, p Project) (Project, error) {
	if _, err := ProjectByName(ctx, h, p.RepoID, p.Name); err == nil {
		return Project{}, db.ErrDuplicateKey
	}
	var id int64
	if err := h.GetContext(ctx, &id, h.Rebind(`INSERT INTO issue_projects (repo_id, name, description, status, lead_id, start_date, target_date)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`), p.RepoID, p.Name, p.Description, p.Status, p.LeadID, p.StartDate, p.TargetDate); err != nil {
		return Project{}, wrap(err)
	}
	return ProjectByID(ctx, h, id)
}

// UpdateProject writes a project's fields.
func UpdateProject(ctx context.Context, h db.Handler, p Project) error {
	_, err := h.ExecContext(ctx, h.Rebind(`UPDATE issue_projects SET name = ?, description = ?, status = ?, lead_id = ?,
		start_date = ?, target_date = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`),
		p.Name, p.Description, p.Status, p.LeadID, p.StartDate, p.TargetDate, p.ID)
	return wrap(err)
}

// DeleteProject removes a project; its issues stay, in no project.
func DeleteProject(ctx context.Context, h db.Handler, id int64) error {
	if _, err := h.ExecContext(ctx, h.Rebind(`UPDATE issues SET project_id = NULL WHERE project_id = ?`), id); err != nil {
		return wrap(err)
	}
	_, err := h.ExecContext(ctx, h.Rebind(`DELETE FROM issue_projects WHERE id = ?`), id)
	return wrap(err)
}

const selectCycle = `SELECT id, repo_id, number, name, start_date, end_date, created_at FROM issue_cycles`

// Cycles returns a repository's cycles, newest first.
func Cycles(ctx context.Context, h db.Handler, repoID int64) ([]Cycle, error) {
	var cs []Cycle
	err := h.SelectContext(ctx, &cs, h.Rebind(selectCycle+` WHERE repo_id = ? ORDER BY start_date DESC, number DESC`), repoID)
	return cs, wrap(err)
}

// CycleByNumber returns one of a repository's cycles.
func CycleByNumber(ctx context.Context, h db.Handler, repoID, number int64) (Cycle, error) {
	var c Cycle
	err := h.GetContext(ctx, &c, h.Rebind(selectCycle+` WHERE repo_id = ? AND number = ?`), repoID, number)
	return c, wrap(err)
}

// CycleOn returns the cycle a day falls in, or ErrNotFound.
func CycleOn(ctx context.Context, h db.Handler, repoID int64, day string) (Cycle, error) {
	var c Cycle
	err := h.GetContext(ctx, &c, h.Rebind(selectCycle+` WHERE repo_id = ? AND start_date <= ? AND end_date >= ? ORDER BY start_date DESC LIMIT 1`), repoID, day, day)
	return c, wrap(err)
}

// CycleAfter returns the first cycle starting after a day, or ErrNotFound.
func CycleAfter(ctx context.Context, h db.Handler, repoID int64, day string) (Cycle, error) {
	var c Cycle
	err := h.GetContext(ctx, &c, h.Rebind(selectCycle+` WHERE repo_id = ? AND start_date > ? ORDER BY start_date LIMIT 1`), repoID, day)
	return c, wrap(err)
}

// CycleBefore returns the last cycle that ended before a day, or
// ErrNotFound.
func CycleBefore(ctx context.Context, h db.Handler, repoID int64, day string) (Cycle, error) {
	var c Cycle
	err := h.GetContext(ctx, &c, h.Rebind(selectCycle+` WHERE repo_id = ? AND end_date < ? ORDER BY end_date DESC LIMIT 1`), repoID, day)
	return c, wrap(err)
}

// OverlappingCycle returns a cycle, other than except, whose dates overlap
// start to end, or ErrNotFound.
func OverlappingCycle(ctx context.Context, h db.Handler, repoID int64, start, end string, except int64) (Cycle, error) {
	var c Cycle
	err := h.GetContext(ctx, &c, h.Rebind(selectCycle+` WHERE repo_id = ? AND id <> ? AND start_date <= ? AND end_date >= ? LIMIT 1`), repoID, except, end, start)
	return c, wrap(err)
}

// CreateCycle adds a cycle with the repository's next cycle number.
func CreateCycle(ctx context.Context, h db.Handler, c Cycle) (Cycle, error) {
	if err := h.GetContext(ctx, &c.Number, h.Rebind(`SELECT COALESCE(MAX(number), 0) + 1 FROM issue_cycles WHERE repo_id = ?`), c.RepoID); err != nil {
		return Cycle{}, wrap(err)
	}
	if _, err := h.ExecContext(ctx, h.Rebind(`INSERT INTO issue_cycles (repo_id, number, name, start_date, end_date) VALUES (?, ?, ?, ?, ?)`),
		c.RepoID, c.Number, c.Name, c.StartDate, c.EndDate); err != nil {
		return Cycle{}, wrap(err)
	}
	return CycleByNumber(ctx, h, c.RepoID, c.Number)
}

// UpdateCycle writes a cycle's name and dates.
func UpdateCycle(ctx context.Context, h db.Handler, c Cycle) error {
	_, err := h.ExecContext(ctx, h.Rebind(`UPDATE issue_cycles SET name = ?, start_date = ?, end_date = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`),
		c.Name, c.StartDate, c.EndDate, c.ID)
	return wrap(err)
}

// DeleteCycle removes a cycle; its issues stay, in no cycle.
func DeleteCycle(ctx context.Context, h db.Handler, id int64) error {
	if _, err := h.ExecContext(ctx, h.Rebind(`UPDATE issues SET cycle_id = NULL WHERE cycle_id = ?`), id); err != nil {
		return wrap(err)
	}
	_, err := h.ExecContext(ctx, h.Rebind(`DELETE FROM issue_cycles WHERE id = ?`), id)
	return wrap(err)
}

// ProgressOf counts the issues in a project or cycle: col is "project_id"
// or "cycle_id".
func ProgressOf(ctx context.Context, h db.Handler, col string, id int64) (Progress, error) {
	if col != "project_id" && col != "cycle_id" {
		panic("issue: ProgressOf on " + col)
	}
	var p Progress
	err := h.GetContext(ctx, &p, h.Rebind(`SELECT
		COALESCE(SUM(CASE WHEN status <> 'canceled' THEN 1 ELSE 0 END), 0) AS total,
		COALESCE(SUM(CASE WHEN status IN ('in_progress', 'in_review') THEN 1 ELSE 0 END), 0) AS started,
		COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0) AS completed,
		COALESCE(SUM(CASE WHEN status = 'canceled' THEN 1 ELSE 0 END), 0) AS canceled
		FROM issues WHERE `+col+` = ?`), id)
	return p, wrap(err)
}
