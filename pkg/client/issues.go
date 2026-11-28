package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/charmbracelet/soft-serve/pkg/ops"
)

var _ ops.IssueService = (*Client)(nil)

func issuePath(repo string, number int64, rest ...string) string {
	return repoPath(repo, append([]string{"issues", strconv.FormatInt(number, 10)}, rest...)...)
}

func issueQueryValues(q ops.IssueQuery) url.Values {
	v := url.Values{}
	for _, s := range q.Status {
		v.Add("status", s)
	}
	for _, p := range q.Priority {
		v.Add("priority", p)
	}
	for _, l := range q.Label {
		v.Add("label", l)
	}
	set := func(k, s string) {
		if s != "" {
			v.Set(k, s)
		}
	}
	set("assignee", q.Assignee)
	set("parent", q.Parent)
	set("project", q.Project)
	set("cycle", q.Cycle)
	set("q", q.Query)
	set("sort", q.Sort)
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	if q.Closed {
		v.Set("closed", "true")
	}
	if q.Description {
		v.Set("description", "true")
	}
	return v
}

// IssueTracker implements ops.IssueService.
func (c *Client) IssueTracker(ctx context.Context, repo string) (ops.IssueTracker, error) {
	var out ops.IssueTracker
	err := c.do(ctx, http.MethodGet, repoPath(repo, "issue-tracker"), nil, nil, &out)
	return out, err
}

// SetIssueKey implements ops.IssueService.
func (c *Client) SetIssueKey(ctx context.Context, repo, key string) error {
	return c.do(ctx, http.MethodPatch, repoPath(repo, "issue-tracker"), nil, map[string]string{"key": key}, nil)
}

// ListIssues implements ops.IssueService.
func (c *Client) ListIssues(ctx context.Context, q ops.IssueQuery) (ops.IssueList, error) {
	var out ops.IssueList
	path := "/v1/issues"
	if q.Repo != "" {
		path = repoPath(q.Repo, "issues")
	}
	err := c.do(ctx, http.MethodGet, path, issueQueryValues(q), nil, &out)
	return out, err
}

// Issue implements ops.IssueService.
func (c *Client) Issue(ctx context.Context, repo string, number int64) (ops.IssueDetail, error) {
	var out ops.IssueDetail
	err := c.do(ctx, http.MethodGet, issuePath(repo, number), nil, nil, &out)
	return out, err
}

// FindIssue implements ops.IssueService.
func (c *Client) FindIssue(ctx context.Context, id string) (string, int64, error) {
	var out ops.IssueDetail
	if err := c.do(ctx, http.MethodGet, "/v1/issues/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return "", 0, err
	}
	return out.Repo, out.Number, nil
}

// CreateIssue implements ops.IssueService.
func (c *Client) CreateIssue(ctx context.Context, repo string, in ops.IssueCreate) (ops.Issue, error) {
	var out ops.Issue
	err := c.do(ctx, http.MethodPost, repoPath(repo, "issues"), nil, in, &out)
	return out, err
}

// UpdateIssue implements ops.IssueService.
func (c *Client) UpdateIssue(ctx context.Context, repo string, number int64, u ops.IssueUpdate) (ops.Issue, error) {
	var out ops.Issue
	err := c.do(ctx, http.MethodPatch, issuePath(repo, number), nil, u, &out)
	return out, err
}

// DeleteIssue implements ops.IssueService.
func (c *Client) DeleteIssue(ctx context.Context, repo string, number int64) error {
	return c.do(ctx, http.MethodDelete, issuePath(repo, number), nil, nil, nil)
}

// AddIssueComment implements ops.IssueService.
func (c *Client) AddIssueComment(ctx context.Context, repo string, number int64, body string) (ops.IssueComment, error) {
	var out ops.IssueComment
	err := c.do(ctx, http.MethodPost, issuePath(repo, number, "comments"), nil, map[string]string{"body": body}, &out)
	return out, err
}

// UpdateIssueComment implements ops.IssueService.
func (c *Client) UpdateIssueComment(ctx context.Context, repo string, number, id int64, body string) (ops.IssueComment, error) {
	var out ops.IssueComment
	err := c.do(ctx, http.MethodPatch, issuePath(repo, number, "comments", strconv.FormatInt(id, 10)), nil, map[string]string{"body": body}, &out)
	return out, err
}

// DeleteIssueComment implements ops.IssueService.
func (c *Client) DeleteIssueComment(ctx context.Context, repo string, number, id int64) error {
	return c.do(ctx, http.MethodDelete, issuePath(repo, number, "comments", strconv.FormatInt(id, 10)), nil, nil, nil)
}

// IssueLabels implements ops.IssueService.
func (c *Client) IssueLabels(ctx context.Context, repo string) ([]ops.IssueLabel, error) {
	var out []ops.IssueLabel
	err := c.do(ctx, http.MethodGet, repoPath(repo, "labels"), nil, nil, &out)
	return out, err
}

// CreateIssueLabel implements ops.IssueService.
func (c *Client) CreateIssueLabel(ctx context.Context, repo string, l ops.IssueLabel) (ops.IssueLabel, error) {
	var out ops.IssueLabel
	err := c.do(ctx, http.MethodPost, repoPath(repo, "labels"), nil, l, &out)
	return out, err
}

// UpdateIssueLabel implements ops.IssueService.
func (c *Client) UpdateIssueLabel(ctx context.Context, repo, name string, u ops.IssueLabelUpdate) (ops.IssueLabel, error) {
	var out ops.IssueLabel
	err := c.do(ctx, http.MethodPatch, repoPath(repo, "labels", url.PathEscape(name)), nil, u, &out)
	return out, err
}

// DeleteIssueLabel implements ops.IssueService.
func (c *Client) DeleteIssueLabel(ctx context.Context, repo, name string) error {
	return c.do(ctx, http.MethodDelete, repoPath(repo, "labels", url.PathEscape(name)), nil, nil, nil)
}

// IssueProjects implements ops.IssueService.
func (c *Client) IssueProjects(ctx context.Context, repo string) ([]ops.IssueProject, error) {
	var out []ops.IssueProject
	err := c.do(ctx, http.MethodGet, repoPath(repo, "projects"), nil, nil, &out)
	return out, err
}

// IssueProject implements ops.IssueService.
func (c *Client) IssueProject(ctx context.Context, repo, name string) (ops.IssueProject, error) {
	var out ops.IssueProject
	err := c.do(ctx, http.MethodGet, repoPath(repo, "projects", url.PathEscape(name)), nil, nil, &out)
	return out, err
}

// CreateIssueProject implements ops.IssueService.
func (c *Client) CreateIssueProject(ctx context.Context, repo string, in ops.IssueProjectInput) (ops.IssueProject, error) {
	var out ops.IssueProject
	err := c.do(ctx, http.MethodPost, repoPath(repo, "projects"), nil, in, &out)
	return out, err
}

// UpdateIssueProject implements ops.IssueService.
func (c *Client) UpdateIssueProject(ctx context.Context, repo, name string, u ops.IssueProjectUpdate) (ops.IssueProject, error) {
	var out ops.IssueProject
	err := c.do(ctx, http.MethodPatch, repoPath(repo, "projects", url.PathEscape(name)), nil, u, &out)
	return out, err
}

// DeleteIssueProject implements ops.IssueService.
func (c *Client) DeleteIssueProject(ctx context.Context, repo, name string) error {
	return c.do(ctx, http.MethodDelete, repoPath(repo, "projects", url.PathEscape(name)), nil, nil, nil)
}

// IssueCycles implements ops.IssueService.
func (c *Client) IssueCycles(ctx context.Context, repo string) ([]ops.IssueCycle, error) {
	var out []ops.IssueCycle
	err := c.do(ctx, http.MethodGet, repoPath(repo, "cycles"), nil, nil, &out)
	return out, err
}

// IssueCycle implements ops.IssueService.
func (c *Client) IssueCycle(ctx context.Context, repo, ref string) (ops.IssueCycle, error) {
	var out ops.IssueCycle
	err := c.do(ctx, http.MethodGet, repoPath(repo, "cycles", url.PathEscape(ref)), nil, nil, &out)
	return out, err
}

// CreateIssueCycle implements ops.IssueService.
func (c *Client) CreateIssueCycle(ctx context.Context, repo string, in ops.IssueCycleInput) (ops.IssueCycle, error) {
	var out ops.IssueCycle
	err := c.do(ctx, http.MethodPost, repoPath(repo, "cycles"), nil, in, &out)
	return out, err
}

// UpdateIssueCycle implements ops.IssueService.
func (c *Client) UpdateIssueCycle(ctx context.Context, repo, ref string, u ops.IssueCycleUpdate) (ops.IssueCycle, error) {
	var out ops.IssueCycle
	err := c.do(ctx, http.MethodPatch, repoPath(repo, "cycles", url.PathEscape(ref)), nil, u, &out)
	return out, err
}

// DeleteIssueCycle implements ops.IssueService.
func (c *Client) DeleteIssueCycle(ctx context.Context, repo, ref string) error {
	return c.do(ctx, http.MethodDelete, repoPath(repo, "cycles", url.PathEscape(ref)), nil, nil, nil)
}
