package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/lfs"
	"github.com/matryer/is"
)

// addCommit commits files on top of main in the bare repository at dir.
func addCommit(t *testing.T, dir, msg string, files map[string]string) {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=B", "GIT_AUTHOR_EMAIL=b@example.com", "GIT_COMMITTER_NAME=B", "GIT_COMMITTER_EMAIL=b@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("clone", "-q", dir, ".")
	for name, body := range files {
		p := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", msg)
	run("push", "-q", "origin", "main")
}

func (f *viewFixture) repoDir(name string) string {
	return filepath.Join(config.FromContext(f.ctx).DataPath, "repos", name+".git")
}

// getJSON decodes a 200 answer from p into v, and returns the status.
func (f *viewFixture) getJSON(t *testing.T, p string, v any) int {
	t.Helper()
	w := f.do(req{path: p})
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
			t.Fatalf("%s: %v: %s", p, err, w.Body.String())
		}
	}
	return w.Code
}

// History can be limited to a file or a directory.
func TestHistoryOfPath(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	addCommit(t, f.repoDir("group/open"), "Touch the guide", map[string]string{"docs/guide.md": "Guide, revised\n"})

	var all, file, dir, other CommitPage
	f.getJSON(t, "/v1/repos/group/open/-/commits", &all)
	f.getJSON(t, "/v1/repos/group/open/-/commits?path=docs/guide.md", &file)
	f.getJSON(t, "/v1/repos/group/open/-/commits?path=docs", &dir)
	f.getJSON(t, "/v1/repos/group/open/-/commits?path=src/main.go", &other)
	is.Equal(all.Total, int64(2))
	is.Equal(file.Total, int64(2))
	is.Equal(file.Path, "docs/guide.md")
	is.Equal(dir.Total, int64(2))
	is.Equal(other.Total, int64(1)) // the guide's commit didn't touch it
	is.Equal(other.Commits[0].Summary, "First commit")
	is.Equal(f.do(req{path: "/v1/repos/group/open/-/commits?path=../x"}).Code, http.StatusBadRequest)

	// The file page's History link is the file's history.
	w := f.do(req{path: "/group/open/-/blob?path=src/main.go"})
	is.True(strings.Contains(w.Body.String(), `href="/group/open/-/commits?path=src%2Fmain.go&amp;ref=main"`))
	w = f.do(req{path: "/group/open/-/commits?path=src%2Fmain.go&ref=main"})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), "History of"))
	is.True(!strings.Contains(w.Body.String(), "Touch the guide"))
}

// A file tracked by Git LFS shows and serves the stored file, not the
// pointer; a pointer whose object never arrived says so.
func TestLFSFiles(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)

	content := strings.Repeat("large text stored in LFS\n", 100)
	sum := sha256.Sum256([]byte(content))
	ptr := lfs.Pointer{Oid: hex.EncodeToString(sum[:]), Size: int64(len(content))}
	missing := lfs.Pointer{Oid: strings.Repeat("ab", 32), Size: 42}
	addCommit(t, f.repoDir("group/open"), "Add LFS files", map[string]string{
		"big.txt":     ptr.String(),
		"missing.bin": missing.String(),
	})
	r, err := f.be.Repository(f.ctx, "group/open")
	is.NoErr(err)
	obj := filepath.Join(config.FromContext(f.ctx).DataPath, "lfs", strconv.FormatInt(r.ID(), 10), "objects", ptr.RelativePath())
	is.NoErr(os.MkdirAll(filepath.Dir(obj), 0o755))
	is.NoErr(os.WriteFile(obj, []byte(content), 0o644))

	var b Blob
	f.getJSON(t, "/v1/repos/group/open/-/blob?path=big.txt", &b)
	is.True(b.LFS)
	is.True(!b.LFSMissing)
	is.Equal(b.Size, int64(len(content)))
	is.Equal(b.Content, content)

	w := f.do(req{path: "/v1/repos/group/open/-/raw?path=big.txt"})
	is.Equal(w.Code, http.StatusOK)
	is.Equal(w.Body.String(), content)
	is.Equal(w.Header().Get("Content-Length"), fmt.Sprint(len(content)))
	is.Equal(w.Header().Get("Content-Type"), "text/plain; charset=utf-8")

	f.getJSON(t, "/v1/repos/group/open/-/blob?path=missing.bin", &b)
	is.True(b.LFS)
	is.True(b.LFSMissing)
	is.True(strings.Contains(b.Content, missing.Oid))
	w = f.do(req{path: "/group/open/-/blob?path=missing.bin"})
	is.True(strings.Contains(w.Body.String(), "never pushed to this server"))
}
