package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/charmbracelet/soft-serve/pkg/ops"
)

// The REST API's account management: the SSH info, pubkey, token and
// set-username commands, for the caller's own account.
//
//	PATCH  /v1/me                 change your username
//	GET    /v1/me/keys            list your SSH keys
//	POST   /v1/me/keys            add one
//	DELETE /v1/me/keys?key=KEY    remove one, by key or SHA256 fingerprint
//	GET    /v1/me/tokens          list your access tokens
//	POST   /v1/me/tokens          create one; the answer holds the token
//	DELETE /v1/me/tokens/ID       delete one
//
// GET /v1/me, the account itself, is in api.go.

// serveAccount answers r if it is an account management request, and
// reports whether it was one. rest is the path after /v1/me.
func serveAccount(w http.ResponseWriter, r *http.Request, svc ops.Service, anonymous bool, rest string) bool {
	c := opsCall{w: w, r: r, svc: svc, anonymous: anonymous}
	ctx := r.Context()

	switch {
	case rest == "":
		if r.Method != http.MethodPatch {
			return false
		}
		var body struct {
			Username *string `json:"username"`
		}
		if c.decode(&body) {
			if body.Username == nil {
				c.fail(ops.Invalid("nothing to change: set username"))
				return true
			}
			c.done(svc.SetUsername(ctx, *body.Username))
		}

	case rest == "/keys":
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			acct, err := svc.Account(ctx)
			c.reply(http.StatusOK, acct.PublicKeys, err)
		case http.MethodPost:
			var body struct {
				Key string `json:"key"`
			}
			if c.decode(&body) {
				k, err := svc.AddPublicKey(ctx, body.Key)
				c.reply(http.StatusCreated, k, err)
			}
		case http.MethodDelete:
			c.done(svc.RemovePublicKey(ctx, r.URL.Query().Get("key")))
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPost, http.MethodDelete)
		}

	case rest == "/tokens":
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			toks, err := svc.AccessTokens(ctx)
			c.reply(http.StatusOK, toks, err)
		case http.MethodPost:
			var opts ops.CreateToken
			if c.decode(&opts) {
				// The token is in this answer and nowhere else.
				w.Header().Set("Cache-Control", "no-store")
				tok, err := svc.CreateAccessToken(ctx, opts)
				c.reply(http.StatusCreated, tok, err)
			}
		default:
			c.notAllowed(http.MethodGet, http.MethodHead, http.MethodPost)
		}

	case strings.HasPrefix(rest, "/tokens/"):
		if c.allow(http.MethodDelete) {
			id, err := strconv.ParseInt(strings.TrimPrefix(rest, "/tokens/"), 10, 64)
			if err != nil {
				c.fail(ops.Invalid("invalid token ID"))
				return true
			}
			c.done(svc.DeleteAccessToken(ctx, id))
		}

	default:
		return false
	}
	return true
}
