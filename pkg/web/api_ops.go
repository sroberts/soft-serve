package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/git"
	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/webhook"
)

// The REST API's repository management: the same operations as the SSH
// repo commands, through the same ops.Service, so the same rules apply.
//
//	POST   /v1/repos                                    create or import
//	GET    /v1/repos/NAME/-/info                        settings and refs
//	PATCH  /v1/repos/NAME                               change settings
//	DELETE /v1/repos/NAME                               delete
//	POST   /v1/repos/NAME/-/rename                      rename
//	POST   /v1/repos/NAME/-/sync                        sync a mirror now
//	DELETE /v1/repos/NAME/-/branches/BRANCH             delete a branch
//	DELETE /v1/repos/NAME/-/tags/TAG                    delete a tag
//	GET    /v1/repos/NAME/-/collaborators               list collaborators
//	POST   /v1/repos/NAME/-/collaborators               add one
//	DELETE /v1/repos/NAME/-/collaborators/USER          remove one
//	GET    /v1/repos/NAME/-/webhooks                    list webhooks
//	POST   /v1/repos/NAME/-/webhooks                    add one
//	PATCH  /v1/repos/NAME/-/webhooks/ID                 change one
//	DELETE /v1/repos/NAME/-/webhooks/ID                 remove one
//	GET    /v1/repos/NAME/-/webhooks/ID/deliveries      list deliveries
//	GET    /v1/repos/NAME/-/webhooks/ID/deliveries/DID  one delivery
//	POST   /v1/repos/NAME/-/webhooks/ID/deliveries/DID/redeliver
//
// Requests that change something are refused from other origins by the
// cross-origin protection the server wraps the API in.

// maxOpsBody bounds a request body; the largest is a webhook with a secret.
const maxOpsBody = 64 << 10

// opsCall is one request to the management API.
type opsCall struct {
	w         http.ResponseWriter
	r         *http.Request
	svc       ops.Service
	anonymous bool
}

// serveOps answers r if it is a management request, and reports whether it
// was one. rest is the path after /v1/repos, without slashes at the ends.
func serveOps(w http.ResponseWriter, r *http.Request, svc ops.Service, anonymous bool, rest string) bool {
	c := opsCall{w: w, r: r, svc: svc, anonymous: anonymous}
	ctx := r.Context()

	if rest == "" {
		if r.Method != http.MethodPost {
			return false
		}
		var opts ops.CreateRepo
		if c.decode(&opts) {
			repo, err := svc.CreateRepo(ctx, opts)
			c.reply(http.StatusCreated, repo, err)
		}
		return true
	}

	name, action, _ := strings.Cut(rest, "/-/")
	switch {
	case action == "":
		switch r.Method {
		case http.MethodPatch:
			var u ops.RepoUpdate
			if c.decode(&u) {
				c.done(svc.UpdateRepo(ctx, name, u))
			}
		case http.MethodDelete:
			c.done(svc.DeleteRepo(ctx, name))
		default:
			return false
		}

	case action == "info":
		if c.allow(http.MethodGet, http.MethodHead) {
			repo, err := svc.Repo(ctx, name)
			c.reply(http.StatusOK, repo, err)
		}

	case action == "rename":
		var body struct {
			Name string `json:"name"`
		}
		if c.allow(http.MethodPost) && c.decode(&body) {
			c.done(svc.RenameRepo(ctx, name, body.Name))
		}

	case action == "sync":
		if c.allow(http.MethodPost) {
			c.done(svc.SyncMirror(ctx, name))
		}

	case strings.HasPrefix(action, "branches/"):
		if c.allow(http.MethodDelete) {
			c.done(svc.DeleteBranch(ctx, name, strings.TrimPrefix(action, "branches/")))
		}

	case strings.HasPrefix(action, "tags/"):
		if c.allow(http.MethodDelete) {
			c.done(svc.DeleteTag(ctx, name, strings.TrimPrefix(action, "tags/")))
		}

	case action == "collaborators":
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			cs, err := svc.Collaborators(ctx, name)
			c.reply(http.StatusOK, cs, err)
		case http.MethodPost:
			var body ops.Collaborator
			if c.decode(&body) {
				c.reply(http.StatusCreated, nil, svc.AddCollaborator(ctx, name, body.Username, body.Access))
			}
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPost)
		}

	case strings.HasPrefix(action, "collaborators/"):
		if c.allow(http.MethodDelete) {
			c.done(svc.RemoveCollaborator(ctx, name, strings.TrimPrefix(action, "collaborators/")))
		}

	case action == "webhooks":
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			hooks, err := svc.Webhooks(ctx, name)
			c.reply(http.StatusOK, hooks, err)
		case http.MethodPost:
			var opts ops.WebhookOptions
			if c.decode(&opts) {
				hook, err := svc.CreateWebhook(ctx, name, opts)
				c.reply(http.StatusCreated, hook, err)
			}
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPost)
		}

	case strings.HasPrefix(action, "webhooks/"):
		c.webhook(name, strings.Split(strings.TrimPrefix(action, "webhooks/"), "/"))

	default:
		return false
	}
	return true
}

// webhook answers the routes under /-/webhooks/ID.
func (c opsCall) webhook(name string, parts []string) {
	ctx := c.r.Context()
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		c.fail(ops.Invalid("invalid webhook ID: %q", parts[0]))
		return
	}

	switch {
	case len(parts) == 1:
		switch c.r.Method {
		case http.MethodPatch:
			var opts ops.WebhookOptions
			if c.decode(&opts) {
				c.done(c.svc.UpdateWebhook(ctx, name, id, opts))
			}
		case http.MethodDelete:
			c.done(c.svc.DeleteWebhook(ctx, name, id))
		default:
			c.notAllowed(http.MethodPatch, http.MethodDelete)
		}
	case len(parts) == 2 && parts[1] == "deliveries":
		if c.allow(http.MethodGet, http.MethodHead) {
			dels, err := c.svc.Deliveries(ctx, name, id)
			c.reply(http.StatusOK, dels, err)
		}
	case len(parts) == 3 && parts[1] == "deliveries":
		if c.allow(http.MethodGet, http.MethodHead) {
			del, err := c.svc.Delivery(ctx, name, id, parts[2])
			c.reply(http.StatusOK, del, err)
		}
	case len(parts) == 4 && parts[1] == "deliveries" && parts[3] == "redeliver":
		if c.allow(http.MethodPost) {
			c.done(c.svc.Redeliver(ctx, name, id, parts[2]))
		}
	default:
		writeAPIError(c.w, http.StatusNotFound, "not found")
	}
}

// allow reports whether the request uses one of methods, answering 405 if
// not.
func (c opsCall) allow(methods ...string) bool {
	for _, m := range methods {
		if c.r.Method == m {
			return true
		}
	}
	c.notAllowed(methods...)
	return false
}

func (c opsCall) notAllowed(methods ...string) {
	c.w.Header().Set("Allow", strings.Join(methods, ", "))
	writeAPIError(c.w, http.StatusMethodNotAllowed, "method not allowed")
}

// decode reads the JSON body into v, answering 4xx if it can't.
func (c opsCall) decode(v any) bool {
	if ct := c.r.Header.Get("Content-Type"); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/json" {
			writeAPIError(c.w, http.StatusUnsupportedMediaType, "send the body as application/json")
			return false
		}
	}
	dec := json.NewDecoder(io.LimitReader(c.r.Body, maxOpsBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeAPIError(c.w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return false
	}
	return true
}

// done answers a request that returns nothing.
func (c opsCall) done(err error) {
	c.reply(http.StatusNoContent, nil, err)
}

// reply answers with v and status, or the error.
func (c opsCall) reply(status int, v any, err error) {
	if err != nil {
		c.fail(err)
		return
	}
	if v == nil {
		c.w.WriteHeader(status)
		return
	}
	c.w.Header().Set("Content-Type", "application/json")
	c.w.WriteHeader(status)
	if c.r.Method == http.MethodHead {
		return
	}
	enc := json.NewEncoder(c.w)
	enc.SetIndent("", "  ")
	enc.Encode(v) //nolint:errcheck
}

func (c opsCall) fail(err error) {
	status := opsStatus(err, c.anonymous)
	msg := err.Error()
	if status == http.StatusInternalServerError {
		log.FromContext(c.r.Context()).Error("api", "method", c.r.Method, "path", c.r.URL.Path, "err", err)
		msg = "internal error"
	}
	if status == http.StatusUnauthorized {
		c.w.Header().Set("WWW-Authenticate", `Basic realm="Soft Serve" charset="UTF-8", Token`)
	}
	writeAPIError(c.w, status, msg)
}

// opsStatus is the HTTP status for an error from an ops.Service. Only the
// errors named here have messages fit to show; everything else is internal.
func opsStatus(err error, anonymous bool) int {
	switch {
	case errors.Is(err, proto.ErrUnauthorized):
		if anonymous {
			return http.StatusUnauthorized
		}
		return http.StatusForbidden
	case errors.Is(err, proto.ErrExceedsAccessLevel):
		return http.StatusForbidden
	case errors.Is(err, proto.ErrRepoNotFound),
		errors.Is(err, proto.ErrUserNotFound),
		errors.Is(err, proto.ErrCollaboratorNotFound),
		errors.Is(err, proto.ErrPublicKeyNotFound),
		errors.Is(err, proto.ErrTokenNotFound),
		errors.Is(err, git.ErrReferenceNotExist),
		errors.Is(err, db.ErrRecordNotFound):
		return http.StatusNotFound
	case errors.Is(err, proto.ErrRepoExist),
		errors.Is(err, proto.ErrCollaboratorExist),
		errors.Is(err, proto.ErrMirrorSyncInProgress),
		errors.Is(err, proto.ErrUserExist),
		errors.Is(err, proto.ErrUserOwnsRepos),
		errors.Is(err, proto.ErrPublicKeyExist),
		errors.Is(err, proto.ErrPublicKeyInUse):
		return http.StatusConflict
	case ops.IsInvalid(err),
		errors.Is(err, proto.ErrInvalidRemote),
		errors.Is(err, proto.ErrNotMirror),
		errors.Is(err, access.ErrInvalidAccessLevel),
		errors.Is(err, webhook.ErrInvalidEvent),
		errors.Is(err, webhook.ErrInvalidContentType):
		return http.StatusBadRequest
	case errors.Is(err, proto.ErrImportFailed),
		errors.Is(err, proto.ErrMirrorSyncFailed):
		return http.StatusBadGateway
	case errors.Is(err, proto.ErrImportsDisabled):
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}
