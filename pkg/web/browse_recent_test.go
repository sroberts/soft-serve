package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/matryer/is"
)

func TestRecent(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addBranchCommit(t, f.repoDir("group/open"), "feature", "On a branch", map[string]string{"x.txt": "x\n"})

	get := func(path, auth string) Recent {
		t.Helper()
		w := f.do(req{path: path, auth: auth})
		is.Equal(w.Code, http.StatusOK)
		var r Recent
		is.NoErr(json.Unmarshal(w.Body.Bytes(), &r))
		return r
	}
	where := func(r Recent) map[string]bool {
		out := map[string]bool{}
		for _, c := range r.Commits {
			out[c.Repo+"@"+c.Branch+":"+c.Summary] = true
		}
		return out
	}

	// Every branch counts, and a private repository only for those who
	// can read it.
	anon := where(get("/v1/recent", ""))
	is.True(anon["group/open@feature:On a branch"])
	is.True(anon["group/open@main:First commit"])
	is.Equal(len(anon), 2)
	owner := where(get("/v1/recent", "Token "+f.ownerToken))
	is.True(owner["secret@main:First commit"])
	is.Equal(len(get("/v1/recent?limit=1", "Token "+f.ownerToken).Commits), 1)

	// Each repository carries its newest commit.
	w := f.do(req{path: "/v1/repos?sort=recent", auth: "Token " + f.ownerToken})
	var repos []RepoInfo
	is.NoErr(json.Unmarshal(w.Body.Bytes(), &repos))
	is.Equal(len(repos), 2)
	for _, r := range repos {
		is.True(r.LastCommit != nil)
	}

	// The home page leads with them; filtering shows only repositories.
	home := f.do(req{path: "/"}).Body.String()
	is.True(strings.Contains(home, "Recent commits"))
	is.True(strings.Contains(home, `href="/group/open/-/tree?ref=feature"`))
	is.True(!strings.Contains(home, "secret"))
	is.Equal(strings.Count(home, "<h1"), 1)
	filtered := f.do(req{path: "/?q=open"}).Body.String()
	is.True(!strings.Contains(filtered, "Recent commits"))
	is.True(strings.Contains(filtered, `class="meta latest"`))
}

func TestSortRecent(t *testing.T) {
	is := is.New(t)
	at := func(s string) *LatestCommit {
		c := newBrief("abc", s, "", "")
		return &LatestCommit{CommitBrief: c}
	}
	rs := []RepoInfo{{Name: "empty"}, {Name: "old", LastCommit: at("100")}, {Name: "new", LastCommit: at("200")}, {Name: "also-empty"}}
	sortRecent(rs)
	names := []string{}
	for _, r := range rs {
		names = append(names, r.Name)
	}
	is.Equal(names, []string{"new", "old", "also-empty", "empty"})
}
