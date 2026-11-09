// Package client is an ops.Service over a Soft Serve server's REST API, for
// the soft command line. The server answers with the same ops.Service it
// uses for its SSH commands, so the client sees what an SSH session would.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/webhook"
)

// Client talks to one server as the holder of one access token.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

var _ ops.Service = (*Client)(nil)

// New returns a client for the server at remote, such as
// https://git.example.com, authenticating with token if it is set.
func New(remote, token string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(remote, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("remote must be an http or https URL, like https://git.example.com: %q", remote)
	}
	return &Client{base: u, token: token, http: &http.Client{Timeout: 10 * time.Minute}}, nil
}

// repoPath is the API path for a repository and, optionally, an action on
// it. Names may contain slashes; each segment is escaped on its own.
func repoPath(name string, action ...string) string {
	p := "/v1/repos/" + escapeSegments(strings.Trim(name, "/"))
	if len(action) > 0 {
		p += "/-/" + strings.Join(action, "/")
	}
	return p
}

func escapeSegments(s string) string {
	parts := strings.Split(s, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// do sends a request and decodes a JSON answer into out, if given.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + path
	u.RawPath = strings.TrimRight(c.base.EscapedPath(), "/") + path
	u.RawQuery = query.Encode()

	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Token "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			return fmt.Errorf("%s %s: %s", method, path, resp.Status)
		}
		return serverError(resp.StatusCode, e.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// sentinels are the errors whose messages the server passes on unchanged;
// mapping them back lets callers use errors.Is as they would on the server.
var sentinels = []error{
	proto.ErrUnauthorized,
	proto.ErrRepoNotFound,
	proto.ErrRepoExist,
	proto.ErrUserNotFound,
	proto.ErrCollaboratorNotFound,
	proto.ErrCollaboratorExist,
	proto.ErrExceedsAccessLevel,
	proto.ErrInvalidRemote,
	proto.ErrImportFailed,
	proto.ErrImportsDisabled,
	proto.ErrNotMirror,
	proto.ErrMirrorSyncFailed,
	proto.ErrMirrorSyncInProgress,
	access.ErrInvalidAccessLevel,
	webhook.ErrInvalidEvent,
	webhook.ErrInvalidContentType,
}

func serverError(status int, msg string) error {
	for _, s := range sentinels {
		if msg == s.Error() {
			return s
		}
	}
	if status == http.StatusBadRequest {
		return ops.Invalid("%s", msg)
	}
	return errors.New(msg)
}

// ListRepos implements ops.Service.
func (c *Client) ListRepos(ctx context.Context, all bool) ([]ops.RepoSummary, error) {
	q := url.Values{}
	if all {
		q.Set("hidden", "true")
	}
	var out []ops.RepoSummary
	err := c.do(ctx, http.MethodGet, "/v1/repos", q, nil, &out)
	return out, err
}

// Repo implements ops.Service.
func (c *Client) Repo(ctx context.Context, name string) (ops.Repo, error) {
	var out ops.Repo
	err := c.do(ctx, http.MethodGet, repoPath(name, "info"), nil, nil, &out)
	return out, err
}

// CreateRepo implements ops.Service.
func (c *Client) CreateRepo(ctx context.Context, opts ops.CreateRepo) (ops.Repo, error) {
	var out ops.Repo
	err := c.do(ctx, http.MethodPost, "/v1/repos", nil, opts, &out)
	return out, err
}

// DeleteRepo implements ops.Service.
func (c *Client) DeleteRepo(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, repoPath(name), nil, nil, nil)
}

// RenameRepo implements ops.Service.
func (c *Client) RenameRepo(ctx context.Context, name, newName string) error {
	return c.do(ctx, http.MethodPost, repoPath(name, "rename"), nil, map[string]string{"name": newName}, nil)
}

// UpdateRepo implements ops.Service.
func (c *Client) UpdateRepo(ctx context.Context, name string, u ops.RepoUpdate) error {
	return c.do(ctx, http.MethodPatch, repoPath(name), nil, u, nil)
}

// SyncMirror implements ops.Service.
func (c *Client) SyncMirror(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, repoPath(name, "sync"), nil, nil, nil)
}

// DeleteBranch implements ops.Service.
func (c *Client) DeleteBranch(ctx context.Context, name, branch string) error {
	return c.do(ctx, http.MethodDelete, repoPath(name, "branches", escapeSegments(branch)), nil, nil, nil)
}

// DeleteTag implements ops.Service.
func (c *Client) DeleteTag(ctx context.Context, name, tag string) error {
	return c.do(ctx, http.MethodDelete, repoPath(name, "tags", escapeSegments(tag)), nil, nil, nil)
}

// Collaborators implements ops.Service.
func (c *Client) Collaborators(ctx context.Context, name string) ([]ops.Collaborator, error) {
	var out []ops.Collaborator
	err := c.do(ctx, http.MethodGet, repoPath(name, "collaborators"), nil, nil, &out)
	return out, err
}

// AddCollaborator implements ops.Service.
func (c *Client) AddCollaborator(ctx context.Context, name, username, level string) error {
	return c.do(ctx, http.MethodPost, repoPath(name, "collaborators"), nil, ops.Collaborator{Username: username, Access: level}, nil)
}

// RemoveCollaborator implements ops.Service.
func (c *Client) RemoveCollaborator(ctx context.Context, name, username string) error {
	return c.do(ctx, http.MethodDelete, repoPath(name, "collaborators", url.PathEscape(username)), nil, nil, nil)
}

func hookPath(name string, id int64, rest ...string) string {
	return repoPath(name, append([]string{"webhooks", strconv.FormatInt(id, 10)}, rest...)...)
}

// Webhooks implements ops.Service.
func (c *Client) Webhooks(ctx context.Context, name string) ([]ops.Webhook, error) {
	var out []ops.Webhook
	err := c.do(ctx, http.MethodGet, repoPath(name, "webhooks"), nil, nil, &out)
	return out, err
}

// CreateWebhook implements ops.Service.
func (c *Client) CreateWebhook(ctx context.Context, name string, opts ops.WebhookOptions) (ops.Webhook, error) {
	var out ops.Webhook
	err := c.do(ctx, http.MethodPost, repoPath(name, "webhooks"), nil, opts, &out)
	return out, err
}

// UpdateWebhook implements ops.Service.
func (c *Client) UpdateWebhook(ctx context.Context, name string, id int64, opts ops.WebhookOptions) error {
	return c.do(ctx, http.MethodPatch, hookPath(name, id), nil, opts, nil)
}

// DeleteWebhook implements ops.Service.
func (c *Client) DeleteWebhook(ctx context.Context, name string, id int64) error {
	return c.do(ctx, http.MethodDelete, hookPath(name, id), nil, nil, nil)
}

// Deliveries implements ops.Service.
func (c *Client) Deliveries(ctx context.Context, name string, id int64) ([]ops.Delivery, error) {
	var out []ops.Delivery
	err := c.do(ctx, http.MethodGet, hookPath(name, id, "deliveries"), nil, nil, &out)
	return out, err
}

// Delivery implements ops.Service.
func (c *Client) Delivery(ctx context.Context, name string, id int64, deliveryID string) (ops.DeliveryDetail, error) {
	var out ops.DeliveryDetail
	err := c.do(ctx, http.MethodGet, hookPath(name, id, "deliveries", url.PathEscape(deliveryID)), nil, nil, &out)
	return out, err
}

// Redeliver implements ops.Service.
func (c *Client) Redeliver(ctx context.Context, name string, id int64, deliveryID string) error {
	return c.do(ctx, http.MethodPost, hookPath(name, id, "deliveries", url.PathEscape(deliveryID), "redeliver"), nil, nil, nil)
}
