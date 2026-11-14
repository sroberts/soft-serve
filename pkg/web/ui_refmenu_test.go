package web

import (
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/matryer/is"
)

// addOldBranch adds a branch "old" to group/open holding only README.md.
func addOldBranch(t *testing.T, f *viewFixture) {
	t.Helper()
	dir := filepath.Join(config.FromContext(f.ctx).DataPath, "repos", "group/open.git")
	git := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"--git-dir", dir}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@example.com", "GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL=a@example.com")
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	blob := git("", "rev-parse", "main:README.md")
	tree := git("100644 blob "+blob+"\tREADME.md\n", "mktree")
	commit := git("", "commit-tree", tree, "-m", "Old")
	git("", "branch", "old", commit)
}

func TestRefSwitcher(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addOldBranch(t, f)

	// Each entry is the same page at the other ref.
	body := f.do(req{path: "/group/open/-/blob?path=src/main.go"}).Body.String()
	is.True(strings.Contains(body, `<details class="refmenu">`))
	is.True(strings.Contains(body, `href="/group/open/-/blob?path=src%2Fmain.go&amp;ref=old&amp;switch=1"`))
	is.True(strings.Contains(body, `href="/group/open/-/blob?path=src%2Fmain.go&amp;ref=main&amp;switch=1" aria-current="true"`))
	is.True(strings.Contains(body, `href="/group/open/-/refs?back=%2Fgroup%2Fopen%2F-%2Fblob%3Fpath%3Dsrc%2Fmain.go"`))

	// A path the other ref lacks lands on its nearest folder that it has,
	// and says so; without the switch flag it's still "not found".
	rec := f.do(req{path: "/group/open/-/blob?path=src/deep/file.go&ref=old&switch=1"})
	is.Equal(rec.Code, http.StatusFound)
	to := rec.Header().Get("Location")
	is.Equal(to, "/group/open/-/tree?ref=old&missing=src%2Fdeep%2Ffile.go")
	page := f.do(req{path: to}).Body.String()
	is.True(strings.Contains(page, "src/deep/file.go isn&#39;t on old, and nor is any folder it was in."))
	is.Equal(f.do(req{path: "/group/open/-/blob?path=src/deep/file.go&ref=old"}).Code, http.StatusNotFound)

	// The refs page remembers where it was opened from.
	refs := f.do(req{path: "/group/open/-/refs?back=%2Fgroup%2Fopen%2F-%2Fcommits%3Fpath%3Dsrc"}).Body.String()
	is.True(strings.Contains(refs, `class="refname" href="/group/open/-/commits?path=src&amp;ref=old"`))
	is.True(strings.Contains(refs, "takes you back to the page you came from"))
	// ...but only a page of this repository.
	refs = f.do(req{path: "/group/open/-/refs?back=%2F%2Fevil.example%2Fgroup%2Fopen%2F-%2Fblob"}).Body.String()
	is.True(strings.Contains(refs, `class="refname" href="/group/open/-/tree?ref=old"`))
	is.True(!strings.Contains(refs, "evil.example"))
}

func TestSwitchURL(t *testing.T) {
	is := is.New(t)
	u := func(s string) *url.URL {
		v, err := url.Parse(s)
		is.NoErr(err)
		return v
	}
	// Keeps the search and drops paging and line selection.
	is.Equal(switchURL("r", u("/r/-/commits?q=fix&page=3&ref=main"), "dev"), "/r/-/commits?q=fix&ref=dev")
	is.Equal(switchURL("r", u("/r/-/blob?path=a.go&lines=4&ref=main"), "dev"), "/r/-/blob?path=a.go&ref=dev&switch=1")
	is.Equal(switchURL("r", u("/r"), "dev"), "/r?ref=dev")
	// A page that isn't about a ref goes to the files.
	is.Equal(switchURL("r", u("/r/-/commit/abc"), "dev"), "/r/-/tree?ref=dev")
	is.Equal(switchURL("g/r", u("/g/r/-/tree?path=x"), "v1"), "/g/r/-/tree?path=x&ref=v1&switch=1")
}

func TestBackURL(t *testing.T) {
	is := is.New(t)
	is.True(backURL("r", "/r/-/blob?path=a") != nil)
	is.True(backURL("r", "/r") != nil)
	for _, bad := range []string{"", "//evil/r/-/blob", "https://evil/r/-/blob", "/other/-/blob", `/\evil`, "/r/-/commit/abc", "r/-/blob"} {
		is.True(backURL("r", bad) == nil) // rejected
	}
}

func TestMissingNotice(t *testing.T) {
	is := is.New(t)
	r := func(missing string) *http.Request {
		req, _ := http.NewRequest(http.MethodGet, "/r/-/tree?missing="+url.QueryEscape(missing), nil)
		return req
	}
	is.Equal(missingNotice(r("src/a.go"), "v1", "src"), "src/a.go isn't on v1. This is the nearest folder that is.")
	is.Equal(missingNotice(r("other/a.go"), "v1", "src"), "") // not below the folder shown
	is.Equal(missingNotice(r("src/../x"), "v1", "src"), "")   // not a clean path
	is.Equal(missingNotice(r(""), "v1", "src"), "")
}
