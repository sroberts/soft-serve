package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/matryer/is"
)

// numbered is n lines, "1" to n.
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(strings.Repeat("x", i%7) + "\n")
	}
	return b.String()
}

// edgeFixture adds a repository with names outside ASCII and files long
// enough to be cut, and an empty one.
func edgeFixture(t *testing.T) *viewFixture {
	t.Helper()
	is := is.New(t)
	f := newViewFixture(t)
	owner, err := f.be.User(f.ctx, "owner")
	is.NoErr(err)
	_, err = f.be.CreateRepository(f.ctx, "edge", owner, proto.RepositoryOptions{})
	is.NoErr(err)
	_, err = f.be.CreateRepository(f.ctx, "void", owner, proto.RepositoryOptions{})
	is.NoErr(err)
	pushFiles(t, filepath.Join(config.FromContext(f.ctx).DataPath, "repos", "edge.git"), map[string]string{
		"文件夹/中文.txt":   "日本語\n",
		"rtl-עברית.md": "שלום\n",
		"long.txt":     numbered(6000),
		"big.txt":      numbered(1500),
	})
	return f
}

// Git quotes names outside ASCII unless told not to, and they were listed
// as "\346\226\207..." and couldn't be opened.
func TestNamesOutsideASCII(t *testing.T) {
	is := is.New(t)
	f := edgeFixture(t)

	w := f.do(req{path: "/v1/repos/edge/-/tree"})
	is.Equal(w.Code, http.StatusOK)
	var tree struct{ Entries []struct{ Name string } }
	is.NoErr(json.Unmarshal(w.Body.Bytes(), &tree))
	var names []string
	for _, e := range tree.Entries {
		names = append(names, e.Name)
	}
	is.True(strings.Contains(strings.Join(names, " "), "文件夹"))
	is.True(strings.Contains(strings.Join(names, " "), "rtl-עברית.md"))

	w = f.do(req{path: "/edge/-/blob?path=" + url.QueryEscape("文件夹/中文.txt")})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), "日本語"))
	w = f.do(req{path: "/edge/-/tree?path=" + url.QueryEscape("文件夹")})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), "中文.txt"))
}

// A long file's page shows its first lines and links to the rest.
func TestLongFilesAreCut(t *testing.T) {
	is := is.New(t)
	f := edgeFixture(t)

	body := f.do(req{path: "/edge/-/blob?path=long.txt"}).Body.String()
	is.True(strings.Contains(body, "Showing the first 5,000 of 6,000 lines"))
	is.True(strings.Contains(body, `id="L5000"`))
	is.True(!strings.Contains(body, `id="L5001"`))
	is.True(strings.Contains(body, "all=1"))

	body = f.do(req{path: "/edge/-/blob?path=long.txt&all=1"}).Body.String()
	is.True(strings.Contains(body, `id="L6000"`))
	is.True(!strings.Contains(body, "Showing the first"))

	// A selection past the cut shows the whole file, so it can be seen.
	body = f.do(req{path: "/edge/-/blob?path=long.txt&lines=5500"}).Body.String()
	is.True(strings.Contains(body, `id="L5500" class="hl"`))

	body = f.do(req{path: "/edge/-/blame?path=long.txt"}).Body.String()
	is.True(strings.Contains(body, "Showing the first 5,000 of 6,000 lines"))
	is.True(!strings.Contains(body, `id="L5001"`))
	is.True(strings.Contains(f.do(req{path: "/edge/-/blame?path=long.txt&all=1"}).Body.String(), `id="L6000"`))

	// Short files aren't cut.
	is.True(!strings.Contains(f.do(req{path: "/edge/-/blob?path=big.txt"}).Body.String(), "Showing the first"))
}

// A file added in one hunk used to come through whole, past the cap on
// each file's diff.
func TestLargeHunksAreCut(t *testing.T) {
	is := is.New(t)
	f := edgeFixture(t)

	var list CommitPage
	is.NoErr(json.Unmarshal(f.do(req{path: "/v1/repos/edge/-/commits"}).Body.Bytes(), &list))
	sha := list.Commits[0].SHA
	var c CommitDetail
	is.NoErr(json.Unmarshal(f.do(req{path: "/v1/repos/edge/-/commits/" + sha}).Body.Bytes(), &c))
	is.Equal(len(c.Files), 4)
	for _, fc := range c.Files {
		switch fc.Path {
		case "long.txt", "big.txt":
			is.True(fc.Truncated)
			is.Equal(fc.Additions > 1000, true) // the counts stay whole
		default:
			is.True(!fc.Truncated)
		}
	}
	is.True(strings.Count(c.Patch, "\n") < 2200)

	// Asked for alone, the file comes whole.
	var one CommitDetail
	is.NoErr(json.Unmarshal(f.do(req{path: "/v1/repos/edge/-/commits/" + sha + "?path=big.txt"}).Body.Bytes(), &one))
	is.Equal(len(one.Files), 1)
	is.True(!one.Files[0].Truncated)
	is.True(strings.Count(one.Patch, "\n") > 1500)
}

func TestErrorPagesSayWhatIsMissing(t *testing.T) {
	is := is.New(t)
	f := edgeFixture(t)

	w := f.do(req{path: "/edge/-/tree?ref=nope"})
	is.Equal(w.Code, http.StatusNotFound)
	body := w.Body.String()
	is.True(strings.Contains(body, "There&#39;s no branch, tag or commit called “nope”."))
	is.True(strings.Contains(body, `href="/edge/-/refs"`))
	is.True(strings.Contains(body, `aria-label="Repository"`)) // the tabs stay
	is.True(!strings.Contains(body, "nope</summary>"))         // not offered as the current ref

	w = f.do(req{path: "/edge/-/commit/0000000000000000000000000000000000000001"})
	is.Equal(w.Code, http.StatusNotFound)
	is.True(strings.Contains(w.Body.String(), "There&#39;s no commit “0000000000000000000000000000000000000001” in this repository."))

	w = f.do(req{path: "/edge/-/commit/zzzz"})
	is.Equal(w.Code, http.StatusBadRequest)
	is.True(strings.Contains(w.Body.String(), "isn&#39;t a commit hash."))
	is.True(!strings.Contains(w.Body.String(), "bad request"))

	w = f.do(req{path: "/edge/-/compare?base=main&head=nope"})
	is.Equal(w.Code, http.StatusNotFound)
	is.True(strings.Contains(w.Body.String(), "There&#39;s no branch, tag or commit called “nope”."))
	is.True(!strings.Contains(w.Body.String(), "not found:"))

	// An empty repository's pages lead to its front page, which says how
	// to push.
	w = f.do(req{path: "/void/-/commits"})
	is.Equal(w.Code, http.StatusSeeOther)
	is.Equal(w.Header().Get("Location"), "/void")

	// Elsewhere: no tabs, the way home.
	w = f.do(req{path: "/nope"})
	is.Equal(w.Code, http.StatusNotFound)
	is.True(strings.Contains(w.Body.String(), "<h1 id=\"error-heading\">Not found</h1>"))
	is.True(!strings.Contains(w.Body.String(), `aria-label="Repository"`))
}

// A server error offers to load the page again.
func TestServerErrorOffersRetry(t *testing.T) {
	is := is.New(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x/-/tree?path=a", nil)
	w := httptest.NewRecorder()
	uiHandler{}.renderError(w, r, &page{SiteName: "Soft Serve"}, http.StatusInternalServerError, pageMessage(errStopScan))
	is.Equal(w.Code, http.StatusInternalServerError)
	is.True(strings.Contains(w.Body.String(), `href="/x/-/tree?path=a">Try again`))
	is.True(strings.Contains(w.Body.String(), "Something went wrong on the server."))
}
