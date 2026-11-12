package web

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/matryer/is"
)

// gitIn runs git against the bare repository at dir.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"--git-dir", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_COMMITTER_NAME=Tagger", "GIT_COMMITTER_EMAIL=tagger@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// addBranchCommit commits files on a new branch made from main.
func addBranchCommit(t *testing.T, dir, branch, msg string, files map[string]string) {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=C", "GIT_AUTHOR_EMAIL=c@example.com", "GIT_COMMITTER_NAME=C", "GIT_COMMITTER_EMAIL=c@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("clone", "-q", dir, ".")
	run("checkout", "-q", "-b", branch)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", msg)
	run("push", "-q", "origin", branch)
}

func TestLastCommits(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addCommit(t, f.repoDir("group/open"), "Touch the guide", map[string]string{"docs/guide.md": "Guide, revised\n"})

	var tree Tree
	f.getJSON(t, "/v1/repos/group/open/-/tree?last_commits=true", &tree)
	is.True(tree.LastCommit != nil)
	is.Equal(tree.LastCommit.Summary, "Touch the guide")
	byName := map[string]*CommitBrief{}
	for _, e := range tree.Entries {
		is.True(e.LastCommit != nil) // every entry was found
		byName[e.Name] = e.LastCommit
	}
	is.Equal(byName["docs"].Summary, "Touch the guide")
	is.Equal(byName["src"].Summary, "First commit")
	is.Equal(byName["README.md"].Summary, "First commit")

	// In a subdirectory, paths are relative to it.
	f.getJSON(t, "/v1/repos/group/open/-/tree?path=src&last_commits=true", &tree)
	is.Equal(tree.LastCommit.Summary, "First commit")
	is.Equal(tree.Entries[0].LastCommit.Summary, "First commit")

	// A directory whose name looks like a glob gets its own last commit,
	// not that of a sibling the glob would match. (Windows can't make one.)
	if runtime.GOOS != "windows" {
		addCommit(t, f.repoDir("group/open"), "Add d*", map[string]string{"d*/f.txt": "x\n"})
		addCommit(t, f.repoDir("group/open"), "Add da", map[string]string{"da/f.txt": "y\n"})
		var glob Tree
		f.getJSON(t, "/v1/repos/group/open/-/tree?path=d%2A&last_commits=true", &glob)
		is.True(glob.LastCommit != nil)
		is.Equal(glob.LastCommit.Summary, "Add d*")
	}

	// Without asking, there are none (it costs a history walk).
	var plain Tree
	f.getJSON(t, "/v1/repos/group/open/-/tree", &plain)
	is.True(plain.LastCommit == nil)

	w := f.do(req{path: "/group/open"})
	is.True(strings.Contains(w.Body.String(), "Touch the guide"))
}

func TestRefsWithDatesAndNotes(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	dir := f.repoDir("group/open")
	gitIn(t, dir, "tag", "v0.1", "main")
	gitIn(t, dir, "tag", "-a", "v1.0", "-m", "Version one\n\nThe first release.", "main")
	addBranchCommit(t, dir, "feature", "Add a feature", map[string]string{"feature.txt": "new\n"})

	var refs Refs
	f.getJSON(t, "/v1/repos/group/open/-/refs", &refs)
	is.Equal(refs.Branches[0].Name, "main") // the default first
	is.Equal(refs.Branches[1].Name, "feature")
	is.Equal(refs.Branches[1].Summary, "Add a feature")
	is.True(!refs.Branches[1].Date.IsZero())

	tags := map[string]RefInfo{}
	for _, tg := range refs.Tags {
		tags[tg.Name] = tg
	}
	is.Equal(len(tags), 2)
	v1 := tags["v1.0"]
	is.True(v1.Annotated)
	is.Equal(v1.Message, "Version one\n\nThe first release.")
	is.Equal(v1.Tagger.Name, "Tagger")
	is.Equal(v1.Commit, refs.Branches[0].Commit) // peeled to the commit
	is.Equal(v1.Summary, "First commit")
	is.True(!tags["v0.1"].Annotated)
	is.Equal(tags["v0.1"].Message, "")

	w := f.do(req{path: "/group/open/-/refs"})
	is.Equal(w.Code, http.StatusOK)
	body := w.Body.String()
	is.True(strings.Contains(body, "The first release."))
	is.True(strings.Contains(body, "Release notes"))
	is.True(strings.Contains(body, `/group/open/-/compare?head=feature`))
}

func TestBlame(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addCommit(t, f.repoDir("group/open"), "Change main", map[string]string{"src/main.go": "package main\n\nfunc main() { println(1) }\n"})

	var b Blame
	f.getJSON(t, "/v1/repos/group/open/-/blame?path=src/main.go", &b)
	is.Equal(b.Path, "src/main.go")
	is.Equal(len(b.Hunks), 2)
	is.Equal(b.Hunks[0].Start, 1)
	is.Equal(b.Hunks[0].Lines, []string{"package main", ""})
	is.Equal(b.Commits[b.Hunks[0].Commit].Summary, "First commit")
	is.Equal(b.Hunks[1].Start, 3)
	is.Equal(b.Commits[b.Hunks[1].Commit].Summary, "Change main")
	is.Equal(b.Commits[b.Hunks[1].Commit].Author, "B")

	is.Equal(f.do(req{path: "/v1/repos/group/open/-/blame?path=data.bin"}).Code, http.StatusBadRequest)
	is.Equal(f.do(req{path: "/v1/repos/group/open/-/blame?path=../x"}).Code, http.StatusBadRequest)
	is.Equal(f.do(req{path: "/v1/repos/group/open/-/blame?path=nope.go"}).Code, http.StatusNotFound)

	w := f.do(req{path: "/group/open/-/blame?path=src/main.go"})
	is.Equal(w.Code, http.StatusOK)
	body := w.Body.String()
	is.True(strings.Contains(body, `id="L3"`))
	is.True(strings.Contains(body, "Change main"))
	is.True(strings.Contains(body, `rowspan="2"`))
	is.True(strings.Contains(body, `<span class="kd">func</span>`)) // highlighted
}

func TestCompare(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addBranchCommit(t, f.repoDir("group/open"), "feature", "Add a feature", map[string]string{"feature.txt": "new\n"})

	var c Compare
	f.getJSON(t, "/v1/repos/group/open/-/compare?base=main&head=feature", &c)
	is.Equal(c.TotalCommits, int64(1))
	is.Equal(c.Commits[0].Summary, "Add a feature")
	is.Equal(len(c.Files), 1)
	is.Equal(c.Files[0].Path, "feature.txt")
	is.Equal(c.Files[0].Status, "added")
	is.True(strings.Contains(c.Patch, "+new"))

	// The other way, there's nothing new; base defaults to the default branch.
	f.getJSON(t, "/v1/repos/group/open/-/compare?base=feature&head=main", &c)
	is.Equal(c.TotalCommits, int64(0))
	is.Equal(len(c.Files), 0)
	f.getJSON(t, "/v1/repos/group/open/-/compare?head=feature", &c)
	is.Equal(c.Base, "main")

	is.Equal(f.do(req{path: "/v1/repos/group/open/-/compare?base=main"}).Code, http.StatusBadRequest)
	is.Equal(f.do(req{path: "/v1/repos/group/open/-/compare?base=main&head=nope"}).Code, http.StatusNotFound)
	is.Equal(f.do(req{path: "/v1/repos/group/open/-/compare?base=--output=x&head=main"}).Code, http.StatusBadRequest)

	w := f.do(req{path: "/group/open/-/compare?base=main&head=feature"})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), "Add a feature"))
	w = f.do(req{path: "/group/open/-/compare?base=main&head=nope"})
	is.Equal(w.Code, http.StatusNotFound)
	is.True(strings.Contains(w.Body.String(), `class="formerror"`)) // the form, with the problem
	is.Equal(f.do(req{path: "/group/open/-/compare"}).Code, http.StatusOK)
}

func TestSearch(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)

	var s SearchResult
	f.getJSON(t, "/v1/repos/group/open/-/search?q=PACKAGE", &s) // ignoring case
	paths := []string{}
	for _, fl := range s.Files {
		paths = append(paths, fl.Path)
	}
	slices.Sort(paths)
	is.Equal(paths, []string{"src/deep/file.go", "src/main.go"})
	is.Equal(s.Files[0].Matches[0].Line, 1)

	f.getJSON(t, "/v1/repos/group/open/-/search?q=no+such+text", &s)
	is.Equal(len(s.Files), 0)
	f.getJSON(t, "/v1/repos/group/open/-/search?q=--output%3Dx", &s) // text, not an option
	is.Equal(len(s.Files), 0)
	f.getJSON(t, "/v1/repos/group/open/-/search?q=.*", &s) // text, not a pattern
	is.Equal(len(s.Files), 0)
	is.Equal(f.do(req{path: "/v1/repos/group/open/-/search?q=x"}).Code, http.StatusBadRequest)
	is.Equal(f.do(req{path: "/v1/repos/secret/-/search?q=secret"}).Code, http.StatusNotFound)

	var rs []RepoInfo
	f.getJSON(t, "/v1/repos?q=OPEN", &rs)
	is.Equal(len(rs), 1)
	f.getJSON(t, "/v1/repos?q=nothing", &rs)
	is.Equal(len(rs), 0)

	w := f.do(req{path: "/group/open/-/search?q=func+main"})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), `/group/open/-/blob?path=src%2Fmain.go&amp;ref=main&amp;lines=3#L3`))
	is.Equal(f.do(req{path: "/group/open/-/search?q=x"}).Code, http.StatusBadRequest)
	w = f.do(req{path: "/?q=nothing"})
	is.True(strings.Contains(w.Body.String(), "No repositories match"))
}

func TestArchive(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)

	for _, base := range []string{"/group/open/-/archive", "/v1/repos/group/open/-/archive"} {
		w := f.do(req{path: base + "?ref=main"})
		is.Equal(w.Code, http.StatusOK)
		is.Equal(w.Header().Get("Content-Type"), "application/gzip")
		is.Equal(w.Header().Get("Content-Disposition"), `attachment; filename=open-main.tar.gz`)
		zr, err := gzip.NewReader(w.Body)
		is.NoErr(err)
		tr := tar.NewReader(zr)
		var names []string
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			is.NoErr(err)
			names = append(names, h.Name)
		}
		is.True(slices.Contains(names, "open-main/src/main.go"))

		w = f.do(req{path: base + "?format=zip"})
		is.Equal(w.Code, http.StatusOK)
		is.Equal(w.Header().Get("Content-Type"), "application/zip")
		zf, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		is.NoErr(err)
		is.True(slices.ContainsFunc(zf.File, func(z *zip.File) bool { return z.Name == "open-main/README.md" }))

		is.Equal(f.do(req{path: base + "?format=rar"}).Code, http.StatusBadRequest)
		is.Equal(f.do(req{path: base + "?ref=nope"}).Code, http.StatusNotFound)
	}
	is.Equal(f.do(req{path: "/secret/-/archive"}).Code, http.StatusNotFound)
	w := f.do(req{path: "/secret/-/archive", auth: "Token " + f.ownerToken})
	is.Equal(w.Code, http.StatusOK)
}

func TestFeed(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addCommit(t, f.repoDir("group/open"), "Touch the guide", map[string]string{"docs/guide.md": "Guide, revised\n"})

	w := f.do(req{path: "/group/open/-/commits.atom"})
	is.Equal(w.Code, http.StatusOK)
	is.Equal(w.Header().Get("Content-Type"), "application/atom+xml; charset=utf-8")
	var feed atomFeed
	is.NoErr(xml.Unmarshal(w.Body.Bytes(), &feed))
	is.Equal(len(feed.Entries), 2)
	is.Equal(feed.Entries[0].Title, "Touch the guide")
	is.True(strings.HasPrefix(feed.Entries[0].Link.Href, "http://"+viewHost+"/group/open/-/commit/"))

	w = f.do(req{path: "/group/open/-/commits.atom?path=src"})
	var srcFeed atomFeed
	is.NoErr(xml.Unmarshal(w.Body.Bytes(), &srcFeed))
	is.Equal(len(srcFeed.Entries), 1)

	w = f.do(req{path: "/group/open/-/commits"})
	is.True(strings.Contains(w.Body.String(), `type="application/atom+xml"`))
	is.Equal(f.do(req{path: "/secret/-/commits.atom"}).Code, http.StatusNotFound)
}

func TestAccessAndCollaborators(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	owner, err := f.be.User(f.ctx, "owner")
	is.NoErr(err)
	// Adding a collaborator sends a webhook naming who did it.
	actx := context.WithValue(f.ctx, proto.ContextKeyUser, owner)
	is.NoErr(f.be.AddCollaborator(actx, "group/open", "stranger", access.ReadOnlyAccess))
	ownerAuth := "Token " + f.ownerToken
	stranger := f.outpost.token(t, "stranger", viewHost, nil)

	var info RepoInfo
	w := f.do(req{path: "/v1/repos/group/open", auth: ownerAuth})
	is.NoErr(json.Unmarshal(w.Body.Bytes(), &info))
	is.Equal(info.Access, "admin-access")
	w = f.do(req{path: "/v1/repos/group/open"})
	is.NoErr(json.Unmarshal(w.Body.Bytes(), &info))
	is.Equal(info.Access, "read-only")

	w = f.do(req{path: "/v1/repos/group/open/-/collaborators", auth: ownerAuth})
	is.Equal(w.Code, http.StatusOK)
	var cs []Collaborator
	is.NoErr(json.Unmarshal(w.Body.Bytes(), &cs))
	is.Equal(cs, []Collaborator{{Username: "stranger", Access: "read-only"}})
	is.Equal(f.do(req{path: "/v1/repos/group/open/-/collaborators", identity: stranger}).Code, http.StatusForbidden)
	// Anonymous callers are asked to sign in, the same for a private
	// repository as for a public one, as over SSH.
	is.Equal(f.do(req{path: "/v1/repos/group/open/-/collaborators"}).Code, http.StatusUnauthorized)
	is.Equal(f.do(req{path: "/v1/repos/secret/-/collaborators"}).Code, http.StatusUnauthorized)
	is.Equal(f.do(req{path: "/v1/repos/secret/-/collaborators", identity: stranger}).Code, http.StatusNotFound)

	w = f.do(req{path: "/group/open", auth: ownerAuth})
	is.True(strings.Contains(w.Body.String(), `/group/open/-/access`))
	is.True(strings.Contains(w.Body.String(), "You&#39;re an admin of this repository."))
	w = f.do(req{path: "/group/open", identity: stranger})
	is.True(!strings.Contains(w.Body.String(), `/group/open/-/access`))
	w = f.do(req{path: "/group/open/-/access", auth: ownerAuth})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), "stranger"))
	is.Equal(f.do(req{path: "/group/open/-/access", identity: stranger}).Code, http.StatusForbidden)
}

func TestMCPNewTools(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addBranchCommit(t, f.repoDir("group/open"), "feature", "Add a feature", map[string]string{"feature.txt": "new\n"})
	t.Setenv("OUTPOST_STORAGE_DIR", t.TempDir())

	call := func(name, args string) map[string]any {
		t.Helper()
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
		hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(body))
		hr.Host = viewHost
		hr.Header.Set("Content-Type", "application/json")
		hr.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, hr)
		var out struct {
			Result struct {
				IsError           bool           `json:"isError"`
				StructuredContent map[string]any `json:"structuredContent"`
			} `json:"result"`
		}
		is.NoErr(json.Unmarshal(w.Body.Bytes(), &out))
		is.True(!out.Result.IsError)
		return out.Result.StructuredContent
	}

	s := call("search_code", `{"repo":"group/open","query":"func main"}`)
	is.Equal(len(s["files"].([]any)), 1)
	c := call("compare_refs", `{"repo":"group/open","head":"feature"}`)
	is.Equal(c["total_commits"], float64(1))
	b := call("blame_file", `{"repo":"group/open","path":"src/main.go"}`)
	is.Equal(len(b["hunks"].([]any)), 1)
	l := call("list_repositories", `{"query":"open"}`)
	is.Equal(len(l["repositories"].([]any)), 1)
	st := call("get_repository_stats", `{"repo":"group/open","owner_email":"a@example.com"}`)
	is.True(st["owner"].(map[string]any)["commits"].(float64) >= 1)
	d := call("list_directory", `{"repo":"group/open","last_commits":true}`)
	is.True(d["last_commit"] != nil)
}

func TestMarkMatches(t *testing.T) {
	is := is.New(t)
	is.Equal(string(markMatches(`a <b>Foo</b> foo`, "foo")), `a &lt;b&gt;<mark>Foo</mark>&lt;/b&gt; <mark>foo</mark>`)
	is.Equal(string(markMatches(`<x>`, "")), `&lt;x&gt;`)
	is.Equal(string(markMatches(`nothing`, "zz")), `nothing`)
}

// A request opens each repository once, however many reads it makes, and
// the compare form's ref list skips the per-branch commit counts.
func TestBrowserReusesOpenedRepo(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addBranchCommit(t, f.repoDir("group/open"), "feature", "On a branch", map[string]string{"x.txt": "x\n"})
	b := newBrowser(f.ctx, nil, "http://"+viewHost)
	_, gr1, err := b.open(f.ctx, "group/open")
	is.NoErr(err)
	_, gr2, err := b.open(f.ctx, "group/open.git")
	is.NoErr(err)
	is.True(gr1 == gr2)
	_, _, err = b.open(f.ctx, "secret")
	is.Equal(err, errNotFound)

	full, err := b.refs(f.ctx, "group/open")
	is.NoErr(err)
	plain, err := b.listRefs(f.ctx, "group/open", false)
	is.NoErr(err)
	is.Equal(len(full.Branches), 2)
	is.Equal(len(plain.Branches), len(full.Branches))
	is.True(full.Branches[1].Ahead != nil)
	is.True(plain.Branches[1].Ahead == nil)
}

// HEAD on a download answers with its headers and builds nothing.
func TestHeadSkipsDownloads(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	for _, p := range []string{
		"/v1/repos/group/open/-/archive?format=zip",
		"/group/open/-/archive?format=tar.gz",
		"/v1/repos/group/open/-/raw?path=src/main.go",
		"/group/open/-/raw?path=data.bin",
	} {
		get := f.do(req{path: p})
		head := f.do(req{method: http.MethodHead, path: p})
		is.Equal(get.Code, http.StatusOK)
		is.Equal(head.Code, http.StatusOK)
		is.True(get.Body.Len() > 0)
		is.Equal(head.Body.Len(), 0) // nothing was generated
		is.Equal(head.Header().Get("Content-Type"), get.Header().Get("Content-Type"))
		is.Equal(head.Header().Get("Content-Disposition"), get.Header().Get("Content-Disposition"))
	}
}
