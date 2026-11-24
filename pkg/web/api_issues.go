package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/soft-serve/pkg/ops"
)

// Issues in the REST API:
//
//	GET    /v1/issues                                 issues in every readable repository
//	GET    /v1/issues/ID                              one issue in full, by identifier (ICE-12)
//	PATCH  /v1/issues/ID                              change it
//	DELETE /v1/issues/ID                              delete it
//	POST   /v1/issues/ID/comments                     comment on it
//	PATCH  /v1/issues/ID/comments/CID                 change a comment
//	DELETE /v1/issues/ID/comments/CID                 delete a comment
//
//	GET    /v1/repos/NAME/-/issues                    a repository's issues
//	POST   /v1/repos/NAME/-/issues                    create one
//	GET    /v1/repos/NAME/-/issues/N                  and the same routes as above, by number
//	GET    /v1/repos/NAME/-/issue-tracker             key, statuses, priorities, labels, counts
//	PATCH  /v1/repos/NAME/-/issue-tracker             change the key: {"key": "ICE"}
//	GET    /v1/repos/NAME/-/labels                    labels
//	POST   /v1/repos/NAME/-/labels                    add one
//	PATCH  /v1/repos/NAME/-/labels/LABEL              change one
//	DELETE /v1/repos/NAME/-/labels/LABEL              delete one
//
// Lists take status, priority and label (repeated or comma-separated),
// assignee (a username, "me" or "none"), parent, q, sort, limit, offset,
// closed=true to include done and canceled issues, and description=true to
// include descriptions.

// issueService is the issue side of an ops service.
func (c opsCall) issueService() (ops.IssueService, bool) {
	s, ok := c.svc.(ops.IssueService)
	if !ok {
		writeAPIError(c.w, http.StatusNotImplemented, "issues aren't available here")
	}
	return s, ok
}

// issues answers the issue routes under a repository.
func (c opsCall) issues(repo, action string) {
	svc, ok := c.issueService()
	if !ok {
		return
	}
	ctx := c.r.Context()
	switch {
	case action == "issues":
		switch c.r.Method {
		case http.MethodGet, http.MethodHead:
			q := issueQuery(c.r.URL.Query())
			q.Repo = repo
			l, err := svc.ListIssues(ctx, q)
			c.reply(http.StatusOK, l, err)
		case http.MethodPost:
			var in ops.IssueCreate
			if c.decode(&in) {
				i, err := svc.CreateIssue(ctx, repo, in)
				c.reply(http.StatusCreated, i, err)
			}
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPost)
		}

	case strings.HasPrefix(action, "issues/"):
		parts := strings.Split(strings.TrimPrefix(action, "issues/"), "/")
		n, err := strconv.ParseInt(strings.TrimPrefix(parts[0], "#"), 10, 64)
		if err != nil || n < 1 {
			c.fail(ops.Invalid("invalid issue number: %q", parts[0]))
			return
		}
		c.issue(svc, repo, n, parts[1:])

	case action == "issue-tracker":
		switch c.r.Method {
		case http.MethodGet, http.MethodHead:
			t, err := svc.IssueTracker(ctx, repo)
			c.reply(http.StatusOK, t, err)
		case http.MethodPatch:
			var body struct {
				Key string `json:"key"`
			}
			if c.decode(&body) {
				if err := svc.SetIssueKey(ctx, repo, body.Key); err != nil {
					c.fail(err)
					return
				}
				t, err := svc.IssueTracker(ctx, repo)
				c.reply(http.StatusOK, t, err)
			}
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPatch)
		}

	case action == "labels":
		switch c.r.Method {
		case http.MethodGet, http.MethodHead:
			ls, err := svc.IssueLabels(ctx, repo)
			c.reply(http.StatusOK, ls, err)
		case http.MethodPost:
			var in ops.IssueLabel
			if c.decode(&in) {
				l, err := svc.CreateIssueLabel(ctx, repo, in)
				c.reply(http.StatusCreated, l, err)
			}
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPost)
		}

	case strings.HasPrefix(action, "labels/"):
		name, err := url.PathUnescape(strings.TrimPrefix(action, "labels/"))
		if err != nil {
			c.fail(ops.Invalid("invalid label name"))
			return
		}
		switch c.r.Method {
		case http.MethodPatch:
			var u ops.IssueLabelUpdate
			if c.decode(&u) {
				l, err := svc.UpdateIssueLabel(ctx, repo, name, u)
				c.reply(http.StatusOK, l, err)
			}
		case http.MethodDelete:
			c.done(svc.DeleteIssueLabel(ctx, repo, name))
		default:
			c.notAllowed(http.MethodPatch, http.MethodDelete)
		}
	}
}

// issue answers the routes under one issue: itself, and its comments.
func (c opsCall) issue(svc ops.IssueService, repo string, n int64, parts []string) {
	ctx := c.r.Context()
	switch {
	case len(parts) == 0:
		switch c.r.Method {
		case http.MethodGet, http.MethodHead:
			d, err := svc.Issue(ctx, repo, n)
			c.reply(http.StatusOK, d, err)
		case http.MethodPatch:
			var u ops.IssueUpdate
			if c.decode(&u) {
				i, err := svc.UpdateIssue(ctx, repo, n, u)
				c.reply(http.StatusOK, i, err)
			}
		case http.MethodDelete:
			c.done(svc.DeleteIssue(ctx, repo, n))
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPatch, http.MethodDelete)
		}
	case len(parts) == 1 && parts[0] == "comments":
		var body struct {
			Body string `json:"body"`
		}
		if c.allow(http.MethodPost) && c.decode(&body) {
			cm, err := svc.AddIssueComment(ctx, repo, n, body.Body)
			c.reply(http.StatusCreated, cm, err)
		}
	case len(parts) == 2 && parts[0] == "comments":
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			c.fail(ops.Invalid("invalid comment ID: %q", parts[1]))
			return
		}
		switch c.r.Method {
		case http.MethodPatch:
			var body struct {
				Body string `json:"body"`
			}
			if c.decode(&body) {
				cm, err := svc.UpdateIssueComment(ctx, repo, n, id, body.Body)
				c.reply(http.StatusOK, cm, err)
			}
		case http.MethodDelete:
			c.done(svc.DeleteIssueComment(ctx, repo, n, id))
		default:
			c.notAllowed(http.MethodPatch, http.MethodDelete)
		}
	default:
		writeAPIError(c.w, http.StatusNotFound, "not found")
	}
}

// serveIssuesByID answers /v1/issues: the list across repositories, and
// issues by identifier. rest is the path after /v1/issues.
func serveIssuesByID(w http.ResponseWriter, r *http.Request, svc *ops.Local, anonymous bool, rest string) {
	c := opsCall{w: w, r: r, svc: svc, anonymous: anonymous}
	if rest == "" {
		if c.allow(http.MethodGet, http.MethodHead) {
			l, err := svc.ListIssues(r.Context(), issueQuery(r.URL.Query()))
			c.reply(http.StatusOK, l, err)
		}
		return
	}
	parts := strings.Split(rest, "/")
	repo, n, err := svc.FindIssue(r.Context(), parts[0])
	if err != nil {
		c.fail(err)
		return
	}
	c.issue(svc, repo, n, parts[1:])
}

// issueQuery reads a list's filters from a query string.
func issueQuery(q url.Values) ops.IssueQuery {
	list := func(key string) []string {
		var out []string
		for _, v := range q[key] {
			for _, s := range strings.Split(v, ",") {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	return ops.IssueQuery{
		Status:      list("status"),
		Priority:    list("priority"),
		Label:       list("label"),
		Assignee:    q.Get("assignee"),
		Parent:      q.Get("parent"),
		Query:       q.Get("q"),
		Sort:        q.Get("sort"),
		Limit:       limit,
		Offset:      offset,
		Closed:      q.Get("closed") == "true",
		Description: q.Get("description") == "true",
	}
}
