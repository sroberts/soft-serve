package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// caller is who a web, REST or MCP request is from.
type caller struct {
	// user is the Soft Serve user whose access applies; nil means anonymous.
	user proto.User
	// outpostUser is the verified Outpost username, when the gateway fronts
	// us and the request carried a valid identity token, and outpostEmail
	// the email it gives for them, if any.
	outpostUser  string
	outpostEmail string
}

var errBadCredentials = errors.New("invalid credentials")

// identifyCaller works out who is calling:
//
//  1. Soft Serve credentials in Authorization (an access token as
//     "Token <token>", or basic auth), when the caller sends them. These pass
//     through Outpost's gateway untouched, and a bad one is refused rather
//     than ignored.
//  2. Behind Outpost's gateway, the verified X-Outpost-Identity token, as the
//     Soft Serve user of the same name. An Outpost user with no Soft Serve
//     account of that name sees what anonymous callers see.
//  3. Otherwise anonymous, with the server's anon-access level.
//
// Soft Serve presents no login of its own on these pages.
func identifyCaller(r *http.Request, v *outpostVerifier) (caller, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); h != "" && !isOutpostToken(h) {
		user, err := parseAuthHdr(r, "")
		if err != nil || user == nil {
			return caller{}, errBadCredentials
		}
		return caller{user: user}, nil
	}
	if v == nil {
		return caller{}, nil
	}
	id, err := v.verifyIdentity(r)
	if err != nil {
		if !errors.Is(err, errNoIdentity) {
			log.FromContext(ctx).Warn("ignoring outpost identity", "err", err)
		}
		return caller{}, nil
	}
	return caller{user: softServeUser(ctx, id.name), outpostUser: id.name, outpostEmail: id.email}, nil
}

// isOutpostToken reports whether an Authorization header holds an Outpost
// access token. The gateway removes these before they reach us, so seeing one
// means the request didn't come through it; it is not ours to check.
func isOutpostToken(h string) bool {
	scheme, tok, _ := strings.Cut(h, " ")
	return strings.EqualFold(scheme, "bearer") && strings.HasPrefix(strings.TrimSpace(tok), "opat_")
}

func softServeUser(ctx context.Context, name string) proto.User {
	u, err := backend.FromContext(ctx).User(ctx, name)
	if err != nil {
		return nil
	}
	return u
}

// cloneBase is where HTTPS clone URLs point, or "" for none. Behind the
// Outpost gateway, git over HTTPS can't authenticate: the gateway wants its
// own login, and Soft Serve's git endpoints don't take Outpost's identity.
// So no HTTPS clone URL is offered there; SSH is the way in.
func cloneBase(r *http.Request, v *outpostVerifier) string {
	if v != nil {
		return ""
	}
	return baseURL(r)
}

// baseURL is the scheme and host the caller reached us at. Behind Outpost
// the node terminates TLS and says so in X-Forwarded-Proto, and Host is
// preserved, so nothing about our address is configured.
func baseURL(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" || strings.ContainsAny(host, "/\\ @") {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p == "https" || p == "http" {
		scheme = p
	}
	return scheme + "://" + host
}
