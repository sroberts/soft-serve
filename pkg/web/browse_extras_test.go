package web

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/soft-serve/git"
	"github.com/matryer/is"
	gossh "golang.org/x/crypto/ssh"
)

func TestParseLines(t *testing.T) {
	is := is.New(t)
	is.Equal(parseLines("12"), lineSel{12, 12})
	is.Equal(parseLines("25-10"), lineSel{10, 25})
	is.Equal(parseLines("0"), lineSel{})
	is.Equal(parseLines("x-3"), lineSel{})
	is.Equal(parseLines(""), lineSel{})
	// One line selected: a click on another makes a range; otherwise a
	// click starts again.
	is.Equal(lineSel{4, 4}.next(9), lineSel{4, 9})
	is.Equal(lineSel{4, 4}.next(2), lineSel{2, 4})
	is.Equal(lineSel{2, 4}.next(9), lineSel{9, 9})
	is.Equal(lineSel{}.next(3), lineSel{3, 3})
}

func TestLineSelectionAndPermalinks(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)

	w := f.do(req{path: "/group/open/-/blob?ref=main&path=src/main.go&lines=1-2"})
	is.Equal(w.Code, http.StatusOK)
	body := w.Body.String()
	is.True(strings.Contains(body, `<tr id="L1" class="hl">`))
	is.True(strings.Contains(body, `<tr id="L2" class="hl">`))
	is.True(strings.Contains(body, `<tr id="L3">`))
	is.True(strings.Contains(body, "lines 1–2 selected"))

	var bl Blob
	f.getJSON(t, "/v1/repos/group/open/-/blob?path=src/main.go", &bl)
	// A permalink pins the commit and keeps the selection.
	is.True(strings.Contains(body, `/group/open/-/blob?path=src%2Fmain.go&amp;ref=`+bl.Commit+`&amp;lines=1-2#L1`))

	// With line 2 alone selected, line 3's number selects 2 to 3.
	w = f.do(req{path: "/group/open/-/blob?ref=main&path=src/main.go&lines=2"})
	is.True(strings.Contains(w.Body.String(), `ref=main&amp;lines=2-3#L2`))

	// Already at a commit: no permalink.
	w = f.do(req{path: "/group/open/-/blob?ref=" + bl.Commit + "&path=src/main.go"})
	is.True(!strings.Contains(w.Body.String(), ">Permalink<"))

	// A Markdown file is rendered, unless its source or lines are asked for.
	w = f.do(req{path: "/group/open/-/blob?path=README.md"})
	is.True(strings.Contains(w.Body.String(), `<div class="markdown">`))
	is.True(strings.Contains(w.Body.String(), "plain=1"))
	w = f.do(req{path: "/group/open/-/blob?path=README.md&lines=1"})
	is.True(strings.Contains(w.Body.String(), `<tr id="L1" class="hl">`))
	is.True(strings.Contains(w.Body.String(), ">Rendered<"))
}

func TestFindFiles(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)

	var fl FileList
	is.Equal(f.getJSON(t, "/v1/repos/group/open/-/files?q=main", &fl), http.StatusOK)
	is.Equal(fl.Files, []string{"src/main.go"})
	fl = FileList{}
	f.getJSON(t, "/v1/repos/group/open/-/files?q=go", &fl)
	is.Equal(fl.Total, 3)
	fl = FileList{}
	// Letters in order: s(rc/)m(ain).g(o)
	f.getJSON(t, "/v1/repos/group/open/-/files?q=smgo", &fl)
	is.Equal(fl.Files, []string{"src/main.go"})
	is.Equal(f.getJSON(t, "/v1/repos/group/open/-/files?q=", &fl), http.StatusBadRequest)
	is.Equal(f.getJSON(t, "/v1/repos/secret/-/files?q=plans", &fl), http.StatusNotFound)

	// The page goes straight to a single match, and lists several.
	w := f.do(req{path: "/group/open/-/find?q=guide"})
	is.Equal(w.Code, http.StatusFound)
	is.Equal(w.Header().Get("Location"), "/group/open/-/blob?path=docs%2Fguide.md")
	w = f.do(req{path: "/group/open/-/find?q=.go"})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), "main<mark>.go</mark>"))
	is.True(strings.Contains(w.Body.String(), "2 files on"))

	// Names beat paths, and closer letters beat spread-out ones.
	a, _ := findScore("src/main.go", "main")
	b, _ := findScore("main/other.go", "main")
	is.True(a < b)
	c, _ := findScore("browse.go", "brgo")
	d, _ := findScore("b/r/x/g/o.txt", "brgo")
	is.True(c < d)
	// A match at the start of the name scores below zero before the tier
	// offset; it must still count.
	r, ok := findScore("readme.md", "rdme")
	is.True(ok)
	is.True(r >= 4)
	_, ok = findScore("abc", "abd")
	is.True(!ok)
	is.Equal(findMarks("pkg/web/browse.go", "brgo"), [][2]int{{8, 10}, {15, 17}})
	is.Equal(string(markRuns("a<b", [][2]int{{1, 2}})), "a<mark>&lt;</mark>b")
}

func TestSearchCommits(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addCommit(t, f.repoDir("group/open"), "Fix the Parser\n\nIt choked on tabs.", map[string]string{"src/parse.go": "package main\n"})
	addBranchCommit(t, f.repoDir("group/open"), "other", "Unrelated", map[string]string{"x.txt": "x\n"})

	var cp CommitPage
	f.getJSON(t, "/v1/repos/group/open/-/commits?q=parser", &cp)
	is.Equal(cp.Total, int64(1))
	is.Equal(cp.Commits[0].Summary, "Fix the Parser")
	is.Equal(cp.Query, "parser")
	cp = CommitPage{}
	// The body counts too, and the query is text, not a pattern.
	f.getJSON(t, "/v1/repos/group/open/-/commits?q=on+tabs", &cp)
	is.Equal(cp.Total, int64(1))
	cp = CommitPage{}
	f.getJSON(t, "/v1/repos/group/open/-/commits?q=.*", &cp)
	is.Equal(cp.Total, int64(0))
	cp = CommitPage{}
	f.getJSON(t, "/v1/repos/group/open/-/commits?author=a@example", &cp)
	is.Equal(cp.Total, int64(1)) // the first commit; addCommit's author is B
	cp = CommitPage{}
	f.getJSON(t, "/v1/repos/group/open/-/commits?q=first&path=src/parse.go", &cp)
	is.Equal(cp.Total, int64(0))
	is.Equal(f.getJSON(t, "/v1/repos/group/open/-/commits?q=x", &cp), http.StatusBadRequest)

	w := f.do(req{path: "/group/open/-/commits?q=parser"})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), "1 commit mentioning “parser”"))
	is.True(strings.Contains(w.Body.String(), "/group/open/-/commits?author=B&amp;ref=main"))
	w = f.do(req{path: "/group/open/-/commits?q=x"})
	is.Equal(w.Code, http.StatusBadRequest)
	is.True(strings.Contains(w.Body.String(), `class="formerror"`))
}

func TestAheadBehind(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addBranchCommit(t, f.repoDir("group/open"), "feature", "Feature work", map[string]string{"f.txt": "f\n"})
	addCommit(t, f.repoDir("group/open"), "Main moves on", map[string]string{"m.txt": "m\n"})

	var refs Refs
	f.getJSON(t, "/v1/repos/group/open/-/refs", &refs)
	for _, b := range refs.Branches {
		switch b.Name {
		case "main":
			is.True(b.Ahead == nil)
		case "feature":
			is.Equal(*b.Ahead, int64(1))
			is.Equal(*b.Behind, int64(1))
		}
	}
	w := f.do(req{path: "/group/open/-/refs"})
	is.True(strings.Contains(w.Body.String(), "1 ahead, 1 behind main"))
	one, zero := int64(1), int64(0)
	is.Equal(distanceText(RefInfo{Ahead: &zero, Behind: &zero}, "main"), "Even with main")
	is.Equal(distanceText(RefInfo{Ahead: &one, Behind: &zero}, "main"), "1 ahead of main")
	is.Equal(distanceText(RefInfo{}, "main"), "")
}

func TestDiffView(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addCommit(t, f.repoDir("group/open"), "Reindent and change", map[string]string{
		"src/main.go":   "package main\n\nfunc main()  {}\n",
		"docs/guide.md": "Guide, changed\n",
	})
	var cp CommitPage
	f.getJSON(t, "/v1/repos/group/open/-/commits?per_page=1", &cp)
	sha := cp.Commits[0].SHA

	var cd CommitDetail
	f.getJSON(t, "/v1/repos/group/open/-/commits/"+sha, &cd)
	is.Equal(len(cd.Files), 2)
	cd = CommitDetail{}
	// Without whitespace, only the real change is left.
	f.getJSON(t, "/v1/repos/group/open/-/commits/"+sha+"?ignore_whitespace=true", &cd)
	is.True(cd.IgnoreWhitespace)
	changed := 0
	for _, fc := range cd.Files {
		changed += fc.Additions
	}
	is.Equal(changed, 1)
	cd = CommitDetail{}
	f.getJSON(t, "/v1/repos/group/open/-/commits/"+sha+"?path=docs/guide.md", &cd)
	is.Equal(len(cd.Files), 1)
	is.Equal(cd.Path, "docs/guide.md")
	is.True(!strings.Contains(cd.Patch, "main.go"))

	w := f.do(req{path: "/group/open/-/commit/" + sha})
	body := w.Body.String()
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(body, `<details class="filediff" id="diff-1" open>`))
	is.True(strings.Contains(body, `href="#diff-2"`))
	is.True(strings.Contains(body, `<tr class="add">`))
	is.True(strings.Contains(body, "?view=split"))
	w = f.do(req{path: "/group/open/-/commit/" + sha + "?view=split&w=1"})
	body = w.Body.String()
	is.True(strings.Contains(body, `class="difftable split"`))
	is.True(strings.Contains(body, "Showing without whitespace changes"))
	// The toggles keep each other.
	is.True(strings.Contains(body, "?view=split\"")) // whitespace off, split kept
	is.True(strings.Contains(body, "?w=1\""))        // unified, whitespace kept

	// Compare has the same view.
	addBranchCommit(t, f.repoDir("group/open"), "feature", "Feature", map[string]string{"f.txt": "a\n"})
	w = f.do(req{path: "/group/open/-/compare?head=feature&view=split"})
	is.True(strings.Contains(w.Body.String(), `class="difftable split"`))
}

func TestPatchLayout(t *testing.T) {
	is := is.New(t)
	patch := "diff --git a/a.txt b/a.txt\nindex 1..2 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -3,3 +3,3 @@ ctx\n same\n-old\n+new\n+more\n\\ No newline at end of file\n" +
		"diff --git a/b.txt b/b.txt\nnew file mode 100644\n--- /dev/null\n+++ b/b.txt\n@@ -0,0 +1 @@\n+b\n"
	chunks := splitPatch(patch)
	is.Equal(len(chunks), 2)
	lines := parseChunk(chunks[0])
	is.Equal(lines[0].Kind, "hunk")
	is.Equal(lines[1], diffLine{Kind: "ctx", Old: 3, New: 3, Text: "same"})
	is.Equal(lines[2], diffLine{Kind: "del", Old: 4, Text: "old"})
	is.Equal(lines[3], diffLine{Kind: "add", New: 4, Text: "new"})
	is.Equal(lines[5].Kind, "note")
	rows := pairLines(lines)
	is.Equal(rows[2].Left.Text, "old")
	is.Equal(rows[2].Right.Text, "new")
	is.True(rows[3].Left == nil)
	is.Equal(rows[3].Right.Text, "more")

	// A cut patch: the last file shown is cut, the ones after aren't shown.
	files := []FileChange{{Path: "a.txt"}, {Path: "b.txt"}, {Path: "c.txt"}}
	v := newDiffView("/r/-/commit/x", url.Values{}, files, patch, true)
	is.True(!v.Files[0].Cut)
	is.True(v.Files[1].Cut)
	is.True(!v.Files[2].Shown)
	is.Equal(v.Files[2].FullURL, "/r/-/commit/x?path=c.txt")
}

// sshSign makes an armored SSH signature of message, as ssh-keygen -Y sign.
func sshSign(t *testing.T, signer gossh.Signer, namespace string, message []byte) string {
	t.Helper()
	h := sha512.Sum512(message)
	signed := append([]byte(sshSigMagic), gossh.Marshal(struct {
		Namespace, Reserved, HashAlg string
		Hash                         []byte
	}{namespace, "", "sha512", h[:]})...)
	sig, err := signer.Sign(rand.Reader, signed)
	if err != nil {
		t.Fatal(err)
	}
	blob := append([]byte(sshSigMagic), gossh.Marshal(struct {
		Version   uint32
		PublicKey []byte
		Namespace string
		Reserved  string
		HashAlg   string
		Signature []byte
	}{1, signer.PublicKey().Marshal(), namespace, "", "sha512", gossh.Marshal(sig)})...)
	b64 := base64.StdEncoding.EncodeToString(blob)
	var out strings.Builder
	out.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for len(b64) > 70 {
		out.WriteString(b64[:70] + "\n")
		b64 = b64[70:]
	}
	out.WriteString(b64 + "\n-----END SSH SIGNATURE-----")
	return out.String()
}

// writeSignedCommit writes a commit on top of main signed with sig (made
// over the commit, or over tamper's version of it) and points branch at it.
func writeSignedCommit(t *testing.T, dir, branch string, sign func(payload []byte) string, tamper func([]byte) []byte) string {
	t.Helper()
	out := func(stdin []byte, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"--git-dir", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		cmd.Stdin = bytes.NewReader(stdin)
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(b))
	}
	tree := out(nil, "rev-parse", "main^{tree}")
	parent := out(nil, "rev-parse", "main")
	when := time.Now().Unix()
	head := "tree " + tree + "\nparent " + parent +
		"\nauthor S <s@example.com> " + strconv.FormatInt(when, 10) + " +0000\ncommitter S <s@example.com> " + strconv.FormatInt(when, 10) + " +0000\n"
	msg := "\nSigned work\n"
	payload := []byte(head + msg)
	sig := sign(payload)
	obj := head + "gpgsig " + strings.ReplaceAll(sig, "\n", "\n ") + "\n" + msg
	if tamper != nil {
		obj = string(tamper([]byte(obj)))
	}
	sha := out([]byte(obj), "hash-object", "-t", "commit", "-w", "--stdin")
	out(nil, "update-ref", "refs/heads/"+branch, sha)
	return sha
}

func TestCommitSignatures(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	dir := f.repoDir("group/open")
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	is.NoErr(err)
	signer, err := gossh.NewSignerFromKey(priv)
	is.NoErr(err)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	other, _ := gossh.NewSignerFromKey(otherPriv)
	is.NoErr(f.be.AddPublicKey(f.ctx, "owner", signer.PublicKey()))

	get := func(sha string) *CommitSignature {
		t.Helper()
		var cd CommitDetail
		is.Equal(f.getJSON(t, "/v1/repos/group/open/-/commits/"+sha, &cd), http.StatusOK)
		return cd.Signature
	}

	good := writeSignedCommit(t, dir, "good", func(p []byte) string { return sshSign(t, signer, "git", p) }, nil)
	s := get(good)
	is.Equal(s.Status, sigVerified)
	is.Equal(s.Signer, "owner")
	is.Equal(s.Key, gossh.FingerprintSHA256(signer.PublicKey()))
	w := f.do(req{path: "/group/open/-/commit/" + good})
	is.True(strings.Contains(w.Body.String(), "✓ Verified"))

	unknown := writeSignedCommit(t, dir, "unknown", func(p []byte) string { return sshSign(t, other, "git", p) }, nil)
	is.Equal(get(unknown).Status, sigUnverified)

	tampered := writeSignedCommit(t, dir, "tampered", func(p []byte) string { return sshSign(t, signer, "git", p) },
		func(b []byte) []byte { return bytes.Replace(b, []byte("Signed work"), []byte("Signed worm"), 1) })
	is.Equal(get(tampered).Status, sigBad)

	wrongNS := writeSignedCommit(t, dir, "wrongns", func(p []byte) string { return sshSign(t, signer, "file", p) }, nil)
	is.Equal(get(wrongNS).Status, sigBad)

	gpg := writeSignedCommit(t, dir, "gpg", func([]byte) string {
		return "-----BEGIN PGP SIGNATURE-----\n\nabc\n-----END PGP SIGNATURE-----"
	}, nil)
	s = get(gpg)
	is.Equal(s.Format, "gpg")
	is.Equal(s.Status, sigUnverified)

	var cp CommitPage
	f.getJSON(t, "/v1/repos/group/open/-/commits", &cp)
	is.True(get(cp.Commits[0].SHA) == nil) // unsigned
}

func TestRedactRemote(t *testing.T) {
	is := is.New(t)
	is.Equal(redactRemote("https://user:secret@example.com/a/b.git?token=x#frag"), "https://example.com/a/b.git")
	is.Equal(redactRemote("ssh://git@example.com:2222/a.git"), "ssh://example.com:2222/a.git")
	is.Equal(redactRemote("git@github.com:owner/repo.git"), "github.com:owner/repo.git")
	is.Equal(redactRemote("/srv/repos/a.git"), "")
	is.Equal(redactRemote("file:///srv/a.git"), "")
	is.Equal(redactRemote(`C:\repos\a`), "")
	is.Equal(redactRemote("./relative"), "")
	// Passwords that keep the URL from parsing must not come back whole.
	for _, raw := range []string{"https://user:pa#ss@example.com/a.git", "https://user:pa/ss@example.com/a.git", "https://user:pa?ss@example.com/a.git"} {
		is.Equal(redactRemote(raw), "")
	}
}

func TestMirrorDetails(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	dir := f.repoDir("group/open")
	gitIn(t, dir, "config", "remote.origin.url", "https://bot:hunter2@example.com/up/stream.git")
	is.NoErr(os.WriteFile(filepath.Join(dir, "FETCH_HEAD"), nil, 0o644))
	is.NoErr(f.be.SetMirror(f.ctx, "group/open", true))

	var info RepoInfo
	f.getJSON(t, "/v1/repos/group/open", &info)
	is.True(info.Mirror)
	is.Equal(info.MirrorOf, "https://example.com/up/stream.git")
	is.True(info.MirrorSyncedAt != nil)
	w := f.do(req{path: "/group/open"})
	is.True(strings.Contains(w.Body.String(), "Mirror of <code>https://example.com/up/stream.git</code>"))
	is.True(!strings.Contains(w.Body.String(), "hunter2"))

	gr, err := git.Open(dir)
	is.NoErr(err)
	from, _ := mirrorDetails(context.Background(), gr)
	is.Equal(from, "https://example.com/up/stream.git")
}

func TestStats(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addCommit(t, f.repoDir("group/open"), "More Go", map[string]string{"vendor/dep/dep.go": strings.Repeat("x", 5000)})

	var st Stats
	is.Equal(f.getJSON(t, "/v1/repos/group/open/-/stats", &st), http.StatusOK)
	is.Equal(st.TotalCommits, int64(2))
	is.Equal(st.Files, 8)
	is.True(len(st.Languages) > 0)
	langs := map[string]LanguageShare{}
	for _, l := range st.Languages {
		langs[l.Name] = l
	}
	is.Equal(langs["Go"].Files, 2) // vendored Go left out
	_, md := langs["Markdown"]
	is.True(!md) // prose isn't code
	is.Equal(len(st.Weekly), 52)
	is.Equal(st.Weekly[51].Commits, 2) // both made this week
	is.Equal(st.Weekly[0].Week.Weekday(), time.Monday)
	is.True(st.TotalContributors >= 1)

	w := f.do(req{path: "/group/open/-/insights"})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), `class="activity"`))
	is.True(strings.Contains(w.Body.String(), "2 commits in the week of"))
	is.Equal(f.do(req{path: "/secret/-/insights"}).Code, http.StatusNotFound)
	is.Equal(percent(0.05), "<0.1%")
	is.Equal(percent(12.345), "12.3%")
}

func TestAccount(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)

	var a Account
	f.getJSON(t, "/v1/me", &a)
	is.True(!a.SignedIn)
	w := f.do(req{path: "/v1/me", auth: "Token " + f.ownerToken})
	is.Equal(w.Code, http.StatusOK)
	is.True(!strings.Contains(w.Body.String(), "hash"))
	is.NoErr(json.Unmarshal(w.Body.Bytes(), &a))
	is.True(a.SignedIn)
	is.Equal(a.Username, "owner")
	is.Equal(len(a.Tokens), 1)
	is.Equal(a.Tokens[0].Name, "t")
	is.True(a.Tokens[0].ExpiresAt != nil && !a.Tokens[0].Expired)
	names := []string{}
	for _, r := range a.Repos {
		names = append(names, r.Name)
	}
	is.Equal(names, []string{"group/open", "secret"})

	// Behind Outpost, a user with no Soft Serve account is told so.
	nobody := f.outpost.token(t, "nobody", viewHost, nil)
	w = f.do(req{path: "/-/me", identity: nobody})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), "no Soft Serve user has that name"))
	w = f.do(req{path: "/-/me", identity: f.outpost.token(t, "owner", viewHost, nil)})
	is.True(strings.Contains(w.Body.String(), "<strong>owner</strong>"))
	is.True(strings.Contains(w.Body.String(), "Repositories you can push to"))
}

func TestMCPExtraTools(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	t.Setenv("OUTPOST_STORAGE_DIR", t.TempDir())

	call := func(name, args, identity string) map[string]any {
		t.Helper()
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
		hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(body))
		hr.Host = viewHost
		hr.Header.Set("Content-Type", "application/json")
		hr.Header.Set("Accept", "application/json, text/event-stream")
		if identity != "" {
			hr.Header.Set(outpostIdentityHeader, identity)
		}
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

	ff := call("find_files", `{"repo":"group/open","query":"guide"}`, "")
	is.Equal(ff["files"], []any{"docs/guide.md"})
	st := call("get_repository_stats", `{"repo":"group/open"}`, "")
	is.Equal(st["total_commits"], float64(1))
	me := call("whoami", `{}`, f.outpost.token(t, "owner", viewHost, nil))
	is.Equal(me["username"], "owner")
	is.Equal(me["outpost_user"], "owner")
	anon := call("whoami", `{}`, "")
	is.Equal(anon["signed_in"], false)
	cs := call("list_commits", `{"repo":"group/open","query":"first"}`, "")
	is.Equal(cs["total"], float64(1))
	cm := call("get_commit", `{"repo":"group/open","sha":"`+cs["commits"].([]any)[0].(map[string]any)["sha"].(string)+`","path":"src/main.go"}`, "")
	is.Equal(len(cm["files"].([]any)), 1)
}
