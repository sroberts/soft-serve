package web

import (
	"net/http"
	"strings"

	"github.com/charmbracelet/soft-serve/pkg/ops"
)

// The REST API's server administration: the SSH user and settings
// commands, for server admins only. Everyone else gets 401 or 403, whether
// or not the user they name exists.
//
//	GET    /v1/users                    list users
//	POST   /v1/users                    create one
//	GET    /v1/users/NAME               one user and their keys
//	PATCH  /v1/users/NAME               change admin or username
//	DELETE /v1/users/NAME               delete one
//	POST   /v1/users/NAME/keys          add a key
//	DELETE /v1/users/NAME/keys?key=KEY  remove one, by key or fingerprint
//	GET    /v1/settings                 the server settings
//	PATCH  /v1/settings                 change them

// serveAdmin answers r if it is an administration request, and reports
// whether it was one.
func serveAdmin(w http.ResponseWriter, r *http.Request, svc ops.Service, anonymous bool) bool {
	c := opsCall{w: w, r: r, svc: svc, anonymous: anonymous}
	ctx := r.Context()
	path := strings.TrimRight(r.URL.Path, "/")

	if path == "/v1/settings" {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s, err := svc.Settings(ctx)
			c.reply(http.StatusOK, s, err)
		case http.MethodPatch:
			var u ops.SettingsUpdate
			if c.decode(&u) {
				s, err := svc.UpdateSettings(ctx, u)
				c.reply(http.StatusOK, s, err)
			}
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPatch)
		}
		return true
	}

	rest, ok := strings.CutPrefix(path, "/v1/users")
	if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
		return false
	}
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			us, err := svc.Users(ctx)
			c.reply(http.StatusOK, us, err)
		case http.MethodPost:
			var opts ops.CreateUser
			if c.decode(&opts) {
				u, err := svc.CreateUser(ctx, opts)
				c.reply(http.StatusCreated, u, err)
			}
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPost)
		}
		return true
	}

	name, sub, _ := strings.Cut(rest, "/")
	switch sub {
	case "":
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			u, err := svc.User(ctx, name)
			c.reply(http.StatusOK, u, err)
		case http.MethodPatch:
			var u ops.UserUpdate
			if c.decode(&u) {
				c.done(svc.UpdateUser(ctx, name, u))
			}
		case http.MethodDelete:
			c.done(svc.DeleteUser(ctx, name))
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPatch, http.MethodDelete)
		}
	case "keys":
		switch r.Method {
		case http.MethodPost:
			var body struct {
				Key string `json:"key"`
			}
			if c.decode(&body) {
				k, err := svc.AddUserKey(ctx, name, body.Key)
				c.reply(http.StatusCreated, k, err)
			}
		case http.MethodDelete:
			c.done(svc.RemoveUserKey(ctx, name, r.URL.Query().Get("key")))
		default:
			c.notAllowed(http.MethodPost, http.MethodDelete)
		}
	default:
		writeAPIError(w, http.StatusNotFound, "not found")
	}
	return true
}
