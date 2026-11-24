package issue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/jmoiron/sqlx"
)

// Tracker is a repository's issue settings: its key and the next number.
type Tracker struct {
	RepoID     int64  `db:"repo_id"`
	Key        string `db:"key"`
	NextNumber int64  `db:"next_number"`
}

// Row is an issue as stored, with the names its IDs stand for.
type Row struct {
	ID          int64         `db:"id"`
	RepoID      int64         `db:"repo_id"`
	Number      int64         `db:"number"`
	Title       string        `db:"title"`
	Description string        `db:"description"`
	Status      string        `db:"status"`
	Priority    int           `db:"priority"`
	AssigneeID  sql.NullInt64 `db:"assignee_id"`
	CreatorID   sql.NullInt64 `db:"creator_id"`
	ParentID    sql.NullInt64 `db:"parent_id"`
	ProjectID   sql.NullInt64 `db:"project_id"`
	CycleID     sql.NullInt64 `db:"cycle_id"`
	SortOrder   float64       `db:"sort_order"`
	CreatedAt   time.Time     `db:"created_at"`
	UpdatedAt   time.Time     `db:"updated_at"`
	StartedAt   sql.NullTime  `db:"started_at"`
	CompletedAt sql.NullTime  `db:"completed_at"`
	CanceledAt  sql.NullTime  `db:"canceled_at"`

	RepoName     string         `db:"repo_name"`
	Key          string         `db:"key"`
	Assignee     sql.NullString `db:"assignee"`
	Creator      sql.NullString `db:"creator"`
	ParentNumber sql.NullInt64  `db:"parent_number"`
	ProjectName  sql.NullString `db:"project_name"`
	CycleNumber  sql.NullInt64  `db:"cycle_number"`
}

// ID is the issue's identifier, such as ICE-12.
func (r Row) Identifier() string { return FormatID(r.Key, r.Number) }

// Label is one of a repository's labels.
type Label struct {
	ID          int64  `db:"id"`
	RepoID      int64  `db:"repo_id"`
	Name        string `db:"name"`
	Color       string `db:"color"`
	Description string `db:"description"`
}

// IssueLabel is a label on an issue.
type IssueLabel struct {
	IssueID int64 `db:"issue_id"`
	Label
}

// Comment is a comment on an issue.
type Comment struct {
	ID        int64          `db:"id"`
	IssueID   int64          `db:"issue_id"`
	AuthorID  sql.NullInt64  `db:"author_id"`
	Author    sql.NullString `db:"author"`
	Body      string         `db:"body"`
	CreatedAt time.Time      `db:"created_at"`
	UpdatedAt time.Time      `db:"updated_at"`
}

// Event is an entry in an issue's activity log: what changed, from what to
// what.
type Event struct {
	ID        int64          `db:"id"`
	IssueID   int64          `db:"issue_id"`
	ActorID   sql.NullInt64  `db:"actor_id"`
	Actor     sql.NullString `db:"actor"`
	Kind      string         `db:"kind"`
	From      string         `db:"from_value"`
	To        string         `db:"to_value"`
	CreatedAt time.Time      `db:"created_at"`
}

// Event kinds.
const (
	EventCreated     = "created"
	EventTitle       = "title"
	EventDescription = "description"
	EventStatus      = "status"
	EventPriority    = "priority"
	EventAssignee    = "assignee"
	EventLabelAdded  = "label_added"
	EventLabelRemove = "label_removed"
	EventParent      = "parent"
	EventProject     = "project"
	EventCycle       = "cycle"
	EventCommit      = "commit"
	EventBranch      = "branch"
)

// Commit is a commit that mentions an issue.
type Commit struct {
	IssueID   int64     `db:"issue_id"`
	RepoID    int64     `db:"repo_id"`
	RepoName  string    `db:"repo_name"`
	SHA       string    `db:"sha"`
	Summary   string    `db:"summary"`
	Closes    bool      `db:"closes"`
	CreatedAt time.Time `db:"created_at"`
}

// ErrNotFound is an issue, label, comment or tracker that isn't there.
var ErrNotFound = errors.New("not found")

func wrap(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return db.WrapError(err)
}

// --- Trackers ---

// TrackerFor returns a repository's tracker, creating it with a key derived
// from the repository's name the first time.
func TrackerFor(ctx context.Context, h db.Handler, repoID int64, repoName string) (Tracker, error) {
	var t Tracker
	err := h.GetContext(ctx, &t, h.Rebind(`SELECT repo_id, key, next_number FROM issue_trackers WHERE repo_id = ?`), repoID)
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return t, wrap(err)
	}
	base := DeriveKey(repoName)
	for i := 1; i < 100; i++ {
		key := base
		if i > 1 {
			suffix := strconv.Itoa(i)
			if len(key)+len(suffix) > 10 {
				key = key[:10-len(suffix)]
			}
			key += suffix
		}
		var taken int
		if err := h.GetContext(ctx, &taken, h.Rebind(`SELECT COUNT(*) FROM issue_trackers WHERE key = ?`), key); err != nil {
			return t, wrap(err)
		}
		if taken > 0 {
			continue
		}
		if _, err := h.ExecContext(ctx, h.Rebind(`INSERT INTO issue_trackers (repo_id, key, next_number) VALUES (?, ?, 1)`), repoID, key); err != nil {
			return t, wrap(err)
		}
		return Tracker{RepoID: repoID, Key: key, NextNumber: 1}, nil
	}
	return t, fmt.Errorf("no free issue key for %q", repoName)
}

// TrackerByKey finds the repository that uses key.
func TrackerByKey(ctx context.Context, h db.Handler, key string) (Tracker, error) {
	var t Tracker
	err := h.GetContext(ctx, &t, h.Rebind(`SELECT repo_id, key, next_number FROM issue_trackers WHERE key = ?`), key)
	return t, wrap(err)
}

// SetKey changes a repository's key. It fails with db.ErrDuplicateKey if
// another repository has it.
func SetKey(ctx context.Context, h db.Handler, repoID int64, key string) error {
	_, err := h.ExecContext(ctx, h.Rebind(`UPDATE issue_trackers SET key = ?, updated_at = CURRENT_TIMESTAMP WHERE repo_id = ?`), key, repoID)
	return wrap(err)
}

// nextNumber takes the next issue number for a repository.
func nextNumber(ctx context.Context, h db.Handler, repoID int64) (int64, error) {
	var n int64
	if err := h.GetContext(ctx, &n, h.Rebind(`UPDATE issue_trackers SET next_number = next_number + 1 WHERE repo_id = ? RETURNING next_number - 1`), repoID); err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

// --- Issues ---

const selectIssue = `SELECT i.id, i.repo_id, i.number, i.title, i.description, i.status, i.priority,
	i.assignee_id, i.creator_id, i.parent_id, i.project_id, i.cycle_id, i.sort_order, i.created_at, i.updated_at,
	i.started_at, i.completed_at, i.canceled_at,
	r.name AS repo_name, t.key AS key, a.username AS assignee, c.username AS creator, p.number AS parent_number,
	pj.name AS project_name, cy.number AS cycle_number
FROM issues i
JOIN repos r ON r.id = i.repo_id
JOIN issue_trackers t ON t.repo_id = i.repo_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN users c ON c.id = i.creator_id
LEFT JOIN issues p ON p.id = i.parent_id
LEFT JOIN issue_projects pj ON pj.id = i.project_id
LEFT JOIN issue_cycles cy ON cy.id = i.cycle_id`

// New is an issue to create.
type New struct {
	Title       string
	Description string
	Status      string
	Priority    int
	AssigneeID  sql.NullInt64
	CreatorID   sql.NullInt64
	ParentID    sql.NullInt64
	ProjectID   sql.NullInt64
	CycleID     sql.NullInt64
}

// Create adds an issue with the repository's next number, and returns it.
func Create(ctx context.Context, h db.Handler, repoID int64, n New) (Row, error) {
	num, err := nextNumber(ctx, h, repoID)
	if err != nil {
		return Row{}, err
	}
	var order float64
	if err := h.GetContext(ctx, &order, h.Rebind(`SELECT COALESCE(MAX(sort_order), 0) + 1 FROM issues WHERE repo_id = ?`), repoID); err != nil {
		return Row{}, wrap(err)
	}
	var id int64
	if err := h.GetContext(ctx, &id, h.Rebind(`INSERT INTO issues (repo_id, number, title, description, status, priority, assignee_id, creator_id, parent_id, project_id, cycle_id, sort_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`),
		repoID, num, n.Title, n.Description, n.Status, n.Priority, n.AssigneeID, n.CreatorID, n.ParentID, n.ProjectID, n.CycleID, order); err != nil {
		return Row{}, wrap(err)
	}
	if err := setStatusTimes(ctx, h, id, n.Status); err != nil {
		return Row{}, err
	}
	return ByID(ctx, h, id)
}

// ByID returns an issue.
func ByID(ctx context.Context, h db.Handler, id int64) (Row, error) {
	var r Row
	err := h.GetContext(ctx, &r, h.Rebind(selectIssue+` WHERE i.id = ?`), id)
	return r, wrap(err)
}

// ByNumber returns a repository's issue by its number.
func ByNumber(ctx context.Context, h db.Handler, repoID, number int64) (Row, error) {
	var r Row
	err := h.GetContext(ctx, &r, h.Rebind(selectIssue+` WHERE i.repo_id = ? AND i.number = ?`), repoID, number)
	return r, wrap(err)
}

// Children returns an issue's sub-issues, by number.
func Children(ctx context.Context, h db.Handler, id int64) ([]Row, error) {
	var rs []Row
	err := h.SelectContext(ctx, &rs, h.Rebind(selectIssue+` WHERE i.parent_id = ? ORDER BY i.number`), id)
	return rs, wrap(err)
}

// Update writes an issue's editable fields as r has them. When the status
// changes, the started, completed and canceled times follow it.
func Update(ctx context.Context, h db.Handler, r Row, statusChanged bool) error {
	if _, err := h.ExecContext(ctx, h.Rebind(`UPDATE issues SET title = ?, description = ?, status = ?, priority = ?,
		assignee_id = ?, parent_id = ?, project_id = ?, cycle_id = ?, sort_order = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`),
		r.Title, r.Description, r.Status, r.Priority, r.AssigneeID, r.ParentID, r.ProjectID, r.CycleID, r.SortOrder, r.ID); err != nil {
		return wrap(err)
	}
	if statusChanged {
		return setStatusTimes(ctx, h, r.ID, r.Status)
	}
	return nil
}

// Touch marks an issue as changed now, for changes stored elsewhere
// (labels, comments).
func Touch(ctx context.Context, h db.Handler, id int64) error {
	_, err := h.ExecContext(ctx, h.Rebind(`UPDATE issues SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`), id)
	return wrap(err)
}

// setStatusTimes keeps started_at, completed_at and canceled_at in step
// with the status, using the database's clock as the other times do.
func setStatusTimes(ctx context.Context, h db.Handler, id int64, status string) error {
	var q string
	switch Category(status) {
	case CategoryStarted:
		q = `UPDATE issues SET started_at = COALESCE(started_at, CURRENT_TIMESTAMP), completed_at = NULL, canceled_at = NULL WHERE id = ?`
	case CategoryCompleted:
		q = `UPDATE issues SET started_at = COALESCE(started_at, CURRENT_TIMESTAMP), completed_at = CURRENT_TIMESTAMP, canceled_at = NULL WHERE id = ?`
	case CategoryCanceled:
		q = `UPDATE issues SET completed_at = NULL, canceled_at = CURRENT_TIMESTAMP WHERE id = ?`
	default:
		q = `UPDATE issues SET started_at = NULL, completed_at = NULL, canceled_at = NULL WHERE id = ?`
	}
	_, err := h.ExecContext(ctx, h.Rebind(q), id)
	return wrap(err)
}

// Delete removes an issue, its comments, activity and links. Its
// sub-issues stay, without a parent.
func Delete(ctx context.Context, h db.Handler, id int64) error {
	_, err := h.ExecContext(ctx, h.Rebind(`DELETE FROM issues WHERE id = ?`), id)
	return wrap(err)
}

// Filter narrows a list of issues. RepoIDs is required.
type Filter struct {
	RepoIDs []int64
	// Statuses, if any, are the statuses to include. Without them, open
	// issues are listed, and closed ones too when Closed is set.
	Statuses []string
	Closed   bool
	// Priorities, if any, are the priorities to include.
	Priorities []int
	// AssigneeID, if set, is the assignee to include; 0 means unassigned.
	AssigneeID *int64
	// Labels are label names an issue must all have.
	Labels []string
	// ParentID, if set, lists that issue's sub-issues.
	ParentID *int64
	// ProjectID and CycleID, if set, are the project and cycle to include;
	// 0 means none.
	ProjectID *int64
	CycleID   *int64
	// Query matches the title, the description or the identifier.
	Query string
	// Sort is priority (the default), updated, created or number.
	Sort   string
	Limit  int
	Offset int
}

// Sorts are the orders a list can be in.
var Sorts = []string{"priority", "updated", "created", "number", "manual"}

// List returns the issues f selects, and how many there are in all.
func List(ctx context.Context, h db.Handler, f Filter) ([]Row, int, error) {
	if len(f.RepoIDs) == 0 {
		return nil, 0, nil
	}
	where := []string{"i.repo_id IN (?)"}
	args := []any{f.RepoIDs}
	switch {
	case len(f.Statuses) > 0:
		where = append(where, "i.status IN (?)")
		args = append(args, f.Statuses)
	case !f.Closed:
		where = append(where, "i.status NOT IN (?)")
		args = append(args, []string{Done, Canceled})
	}
	if len(f.Priorities) > 0 {
		where = append(where, "i.priority IN (?)")
		args = append(args, f.Priorities)
	}
	if f.AssigneeID != nil {
		if *f.AssigneeID == 0 {
			where = append(where, "i.assignee_id IS NULL")
		} else {
			where = append(where, "i.assignee_id = ?")
			args = append(args, *f.AssigneeID)
		}
	}
	for _, l := range f.Labels {
		where = append(where, `EXISTS (SELECT 1 FROM issue_label_links ll JOIN issue_labels lb ON lb.id = ll.label_id
			WHERE ll.issue_id = i.id AND LOWER(lb.name) = ?)`)
		args = append(args, strings.ToLower(l))
	}
	if f.ParentID != nil {
		where = append(where, "i.parent_id = ?")
		args = append(args, *f.ParentID)
	}
	for col, v := range map[string]*int64{"i.project_id": f.ProjectID, "i.cycle_id": f.CycleID} {
		switch {
		case v == nil:
		case *v == 0:
			where = append(where, col+" IS NULL")
		default:
			where = append(where, col+" = ?")
			args = append(args, *v)
		}
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + escapeLike(strings.ToLower(q)) + "%"
		cond := `(LOWER(i.title) LIKE ? ESCAPE '\' OR LOWER(i.description) LIKE ? ESCAPE '\'`
		args = append(args, like, like)
		if key, num, ok := ParseID(q); ok {
			cond += " OR (t.key = ? AND i.number = ?)"
			args = append(args, key, num)
		}
		where = append(where, cond+")")
	}
	cond := " WHERE " + strings.Join(where, " AND ")

	countQ, countArgs, err := sqlx.In(`SELECT COUNT(*) FROM issues i JOIN issue_trackers t ON t.repo_id = i.repo_id`+cond, args...)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := h.GetContext(ctx, &total, h.Rebind(countQ), countArgs...); err != nil {
		return nil, 0, wrap(err)
	}

	order := ` ORDER BY CASE WHEN i.priority = 0 THEN 5 ELSE i.priority END, i.updated_at DESC, i.id DESC`
	switch f.Sort {
	case "updated":
		order = ` ORDER BY i.updated_at DESC, i.id DESC`
	case "created":
		order = ` ORDER BY i.created_at DESC, i.id DESC`
	case "number":
		order = ` ORDER BY r.name, i.number DESC`
	case "manual":
		order = ` ORDER BY i.sort_order, i.id`
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q, qargs, err := sqlx.In(selectIssue+cond+order+fmt.Sprintf(" LIMIT %d OFFSET %d", limit, max(f.Offset, 0)), args...)
	if err != nil {
		return nil, 0, err
	}
	var rs []Row
	if err := h.SelectContext(ctx, &rs, h.Rebind(q), qargs...); err != nil {
		return nil, 0, wrap(err)
	}
	return rs, total, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// --- Labels ---

// Labels returns a repository's labels, by name.
func Labels(ctx context.Context, h db.Handler, repoID int64) ([]Label, error) {
	var ls []Label
	err := h.SelectContext(ctx, &ls, h.Rebind(`SELECT id, repo_id, name, color, description FROM issue_labels WHERE repo_id = ? ORDER BY LOWER(name)`), repoID)
	return ls, wrap(err)
}

// LabelByName returns one of a repository's labels, ignoring case.
func LabelByName(ctx context.Context, h db.Handler, repoID int64, name string) (Label, error) {
	var l Label
	err := h.GetContext(ctx, &l, h.Rebind(`SELECT id, repo_id, name, color, description FROM issue_labels WHERE repo_id = ? AND LOWER(name) = ?`), repoID, strings.ToLower(name))
	return l, wrap(err)
}

// CreateLabel adds a label. It fails with db.ErrDuplicateKey if the
// repository has one by that name.
func CreateLabel(ctx context.Context, h db.Handler, l Label) (Label, error) {
	if _, err := LabelByName(ctx, h, l.RepoID, l.Name); err == nil {
		return Label{}, db.ErrDuplicateKey
	}
	err := h.GetContext(ctx, &l.ID, h.Rebind(`INSERT INTO issue_labels (repo_id, name, color, description) VALUES (?, ?, ?, ?) RETURNING id`),
		l.RepoID, l.Name, l.Color, l.Description)
	return l, wrap(err)
}

// UpdateLabel writes a label's name, color and description.
func UpdateLabel(ctx context.Context, h db.Handler, l Label) error {
	_, err := h.ExecContext(ctx, h.Rebind(`UPDATE issue_labels SET name = ?, color = ?, description = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`),
		l.Name, l.Color, l.Description, l.ID)
	return wrap(err)
}

// DeleteLabel removes a label from the repository and its issues.
func DeleteLabel(ctx context.Context, h db.Handler, id int64) error {
	_, err := h.ExecContext(ctx, h.Rebind(`DELETE FROM issue_labels WHERE id = ?`), id)
	return wrap(err)
}

// LabelsOf returns the labels on each of the issues, by issue ID.
func LabelsOf(ctx context.Context, h db.Handler, issueIDs []int64) (map[int64][]Label, error) {
	out := map[int64][]Label{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	q, args, err := sqlx.In(`SELECT ll.issue_id, l.id, l.repo_id, l.name, l.color, l.description
		FROM issue_label_links ll JOIN issue_labels l ON l.id = ll.label_id
		WHERE ll.issue_id IN (?) ORDER BY LOWER(l.name)`, issueIDs)
	if err != nil {
		return nil, err
	}
	var ls []IssueLabel
	if err := h.SelectContext(ctx, &ls, h.Rebind(q), args...); err != nil {
		return nil, wrap(err)
	}
	for _, l := range ls {
		out[l.IssueID] = append(out[l.IssueID], l.Label)
	}
	return out, nil
}

// AddLabel puts a label on an issue; it does nothing if it's there.
func AddLabel(ctx context.Context, h db.Handler, issueID, labelID int64) (bool, error) {
	var n int
	if err := h.GetContext(ctx, &n, h.Rebind(`SELECT COUNT(*) FROM issue_label_links WHERE issue_id = ? AND label_id = ?`), issueID, labelID); err != nil {
		return false, wrap(err)
	}
	if n > 0 {
		return false, nil
	}
	_, err := h.ExecContext(ctx, h.Rebind(`INSERT INTO issue_label_links (issue_id, label_id) VALUES (?, ?)`), issueID, labelID)
	return err == nil, wrap(err)
}

// RemoveLabel takes a label off an issue, and reports whether it was on.
func RemoveLabel(ctx context.Context, h db.Handler, issueID, labelID int64) (bool, error) {
	res, err := h.ExecContext(ctx, h.Rebind(`DELETE FROM issue_label_links WHERE issue_id = ? AND label_id = ?`), issueID, labelID)
	if err != nil {
		return false, wrap(err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// --- Comments ---

const selectComment = `SELECT c.id, c.issue_id, c.author_id, u.username AS author, c.body, c.created_at, c.updated_at
FROM issue_comments c LEFT JOIN users u ON u.id = c.author_id`

// Comments returns an issue's comments, oldest first.
func Comments(ctx context.Context, h db.Handler, issueID int64) ([]Comment, error) {
	var cs []Comment
	err := h.SelectContext(ctx, &cs, h.Rebind(selectComment+` WHERE c.issue_id = ? ORDER BY c.created_at, c.id`), issueID)
	return cs, wrap(err)
}

// CommentByID returns one of an issue's comments.
func CommentByID(ctx context.Context, h db.Handler, issueID, id int64) (Comment, error) {
	var c Comment
	err := h.GetContext(ctx, &c, h.Rebind(selectComment+` WHERE c.issue_id = ? AND c.id = ?`), issueID, id)
	return c, wrap(err)
}

// AddComment adds a comment and returns it.
func AddComment(ctx context.Context, h db.Handler, issueID int64, authorID sql.NullInt64, body string) (Comment, error) {
	var id int64
	if err := h.GetContext(ctx, &id, h.Rebind(`INSERT INTO issue_comments (issue_id, author_id, body) VALUES (?, ?, ?) RETURNING id`), issueID, authorID, body); err != nil {
		return Comment{}, wrap(err)
	}
	return CommentByID(ctx, h, issueID, id)
}

// UpdateComment changes a comment's text.
func UpdateComment(ctx context.Context, h db.Handler, id int64, body string) error {
	_, err := h.ExecContext(ctx, h.Rebind(`UPDATE issue_comments SET body = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`), body, id)
	return wrap(err)
}

// DeleteComment removes a comment.
func DeleteComment(ctx context.Context, h db.Handler, id int64) error {
	_, err := h.ExecContext(ctx, h.Rebind(`DELETE FROM issue_comments WHERE id = ?`), id)
	return wrap(err)
}

// CommentCounts returns how many comments each issue has.
func CommentCounts(ctx context.Context, h db.Handler, issueIDs []int64) (map[int64]int, error) {
	out := map[int64]int{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	q, args, err := sqlx.In(`SELECT issue_id, COUNT(*) AS n FROM issue_comments WHERE issue_id IN (?) GROUP BY issue_id`, issueIDs)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		IssueID int64 `db:"issue_id"`
		N       int   `db:"n"`
	}
	if err := h.SelectContext(ctx, &rows, h.Rebind(q), args...); err != nil {
		return nil, wrap(err)
	}
	for _, r := range rows {
		out[r.IssueID] = r.N
	}
	return out, nil
}

// --- Activity ---

// Log records a change to an issue.
func Log(ctx context.Context, h db.Handler, issueID int64, actorID sql.NullInt64, kind, from, to string) error {
	_, err := h.ExecContext(ctx, h.Rebind(`INSERT INTO issue_events (issue_id, actor_id, kind, from_value, to_value) VALUES (?, ?, ?, ?, ?)`),
		issueID, actorID, kind, from, to)
	return wrap(err)
}

// Events returns an issue's activity, oldest first.
func Events(ctx context.Context, h db.Handler, issueID int64) ([]Event, error) {
	var es []Event
	err := h.SelectContext(ctx, &es, h.Rebind(`SELECT e.id, e.issue_id, e.actor_id, u.username AS actor, e.kind, e.from_value, e.to_value, e.created_at
		FROM issue_events e LEFT JOIN users u ON u.id = e.actor_id WHERE e.issue_id = ? ORDER BY e.created_at, e.id`), issueID)
	return es, wrap(err)
}

// --- Commits ---

// LinkCommit records that a commit mentions an issue, and reports whether
// it's new. A link that closes replaces one that doesn't.
func LinkCommit(ctx context.Context, h db.Handler, c Commit) (bool, error) {
	var closes []bool
	if err := h.SelectContext(ctx, &closes, h.Rebind(`SELECT closes FROM issue_commits WHERE issue_id = ? AND repo_id = ? AND sha = ?`), c.IssueID, c.RepoID, c.SHA); err != nil {
		return false, wrap(err)
	}
	if len(closes) > 0 {
		if c.Closes && !closes[0] {
			_, err := h.ExecContext(ctx, h.Rebind(`UPDATE issue_commits SET closes = ? WHERE issue_id = ? AND repo_id = ? AND sha = ?`), true, c.IssueID, c.RepoID, c.SHA)
			return false, wrap(err)
		}
		return false, nil
	}
	_, err := h.ExecContext(ctx, h.Rebind(`INSERT INTO issue_commits (issue_id, repo_id, sha, summary, closes) VALUES (?, ?, ?, ?, ?)`),
		c.IssueID, c.RepoID, c.SHA, c.Summary, c.Closes)
	return err == nil, wrap(err)
}

// Commits returns the commits that mention an issue, newest first.
func Commits(ctx context.Context, h db.Handler, issueID int64) ([]Commit, error) {
	var cs []Commit
	err := h.SelectContext(ctx, &cs, h.Rebind(`SELECT ic.issue_id, ic.repo_id, r.name AS repo_name, ic.sha, ic.summary, ic.closes, ic.created_at
		FROM issue_commits ic JOIN repos r ON r.id = ic.repo_id WHERE ic.issue_id = ? ORDER BY ic.created_at DESC, ic.id DESC`), issueID)
	return cs, wrap(err)
}
