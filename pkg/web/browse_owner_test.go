package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/matryer/is"
)

// The fixture's commits are by a@example.com; Outpost's sign-in gives the
// email that marks the caller's own.
func TestInsightsOwnerSplit(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	o := f.outpost
	withEmail := func(email string) string {
		return o.token(t, "owner", viewHost, func(c *jwt.MapClaims) { (*c)["email"] = email })
	}
	stats := func(r req) Stats {
		t.Helper()
		var s Stats
		is.NoErr(json.Unmarshal(f.do(r).Body.Bytes(), &s))
		return s
	}

	// Signed in with the author's email (in any case): every commit is yours.
	s := stats(req{path: "/v1/repos/group/open/-/stats", identity: withEmail("A@Example.com")})
	is.True(s.Owner != nil)
	is.Equal(s.Owner.Commits, s.TotalCommits)
	is.Equal(s.Owner.Others, int64(0))
	is.True(s.Owner.First != nil)
	weekly := 0
	for _, w := range s.Weekly {
		weekly += w.Owner
	}
	is.Equal(int64(weekly), s.TotalCommits)

	page := f.do(req{path: "/group/open/-/insights", identity: withEmail("a@example.com")}).Body.String()
	is.True(strings.Contains(page, "Your commits"))
	is.True(strings.Contains(page, `class="bar mine"`))
	is.True(strings.Contains(page, "All 1 contributor"))

	// Someone else's email: none are yours.
	s = stats(req{path: "/v1/repos/group/open/-/stats", identity: withEmail("b@example.com")})
	is.Equal(s.Owner.Commits, int64(0))
	is.Equal(s.Owner.Others, s.TotalCommits)
	is.True(s.Owner.First == nil)

	// No email from the sign-in: no split, and the page says why.
	s = stats(req{path: "/v1/repos/group/open/-/stats", identity: o.token(t, "owner", viewHost, nil)})
	is.True(s.Owner == nil)
	page = f.do(req{path: "/group/open/-/insights", identity: o.token(t, "owner", viewHost, nil)}).Body.String()
	is.True(strings.Contains(page, "didn't give an email for you"))
	is.True(strings.Contains(page, "Top contributors"))

	// Anonymous: as before, with no note.
	page = f.do(req{path: "/group/open/-/insights"}).Body.String()
	is.True(!strings.Contains(page, "Your commits"))
	is.True(!strings.Contains(page, "give an email"))

	// The API can name the emails itself.
	s = stats(req{path: "/v1/repos/group/open/-/stats?owner_email=nobody@example.com,a@example.com"})
	is.Equal(s.Owner.Commits, s.TotalCommits)

	// Signed in under another address that author_aliases groups with the
	// author's: every commit is still yours, from the page, the API and MCP.
	config.FromContext(f.ctx).HTTP.AuthorAliases = []string{"me@home.org, A@example.com"}
	s = stats(req{path: "/v1/repos/group/open/-/stats", identity: withEmail("me@home.org")})
	is.Equal(s.Owner.Commits, s.TotalCommits)
	s = stats(req{path: "/v1/repos/group/open/-/stats?owner_email=me@home.org"})
	is.Equal(s.Owner.Commits, s.TotalCommits)
	page = f.do(req{path: "/group/open/-/insights", identity: withEmail("me@home.org")}).Body.String()
	is.True(strings.Contains(page, "Your commits"))
}

func TestWithAliases(t *testing.T) {
	is := is.New(t)
	cfg := config.DefaultConfig()
	cfg.HTTP.AuthorAliases = []string{"a@x.com, a@laptop.local", "b@y.org,b@old.org", "junk"}
	ctx := config.WithContext(context.Background(), cfg)
	is.Equal(ownerFor(ctx, "A@x.com"), []string{"a@x.com", "a@laptop.local"})
	is.Equal(ownerFor(ctx, "b@old.org"), []string{"b@old.org", "b@y.org"})
	is.Equal(ownerFor(ctx, "c@z.net"), []string{"c@z.net"})
	is.True(ownerFor(ctx, "") == nil)
}

func TestOwnerEmails(t *testing.T) {
	is := is.New(t)
	is.Equal(ownerEmails(" A@x.com , a@x.com,b@y.org", "", "not-an-email"), []string{"a@x.com", "b@y.org"})
	is.Equal(len(ownerEmails("1@a,2@a,3@a,4@a,5@a,6@a")), 5)
	is.True(ownerEmails("") == nil)
}
