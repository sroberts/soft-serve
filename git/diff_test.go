package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matryer/is"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@example.com",
		"GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL=a@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func diffNames(d *Diff) []string {
	var out []string
	for _, f := range d.Files {
		out = append(out, f.Name)
	}
	return out
}

func TestDiffWithPath(t *testing.T) {
	is := is.New(t)
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFiles(t, dir, map[string]string{
		"a.txt":        "a\n",
		"old/name.txt": strings.Repeat("a long enough line to be found as a rename\n", 20),
		"[ab].txt":     "odd\n",
	})
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "first")
	first := gitIn(t, dir, "rev-parse", "HEAD")

	// A commit with more files than a diff shows, a rename and a change.
	many := map[string]string{"a.txt": "a, changed\n", "[ab].txt": "odd, changed\n"}
	for i := range DiffMaxFiles + 5 {
		many[fmt.Sprintf("gen/%04d.txt", i)] = "x\n"
	}
	writeFiles(t, dir, many)
	is.NoErr(os.MkdirAll(filepath.Join(dir, "new"), 0o755))
	gitIn(t, dir, "mv", "old/name.txt", "new/name.txt")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "second")
	second := gitIn(t, dir, "rev-parse", "HEAD")

	r, err := Open(dir)
	is.NoErr(err)

	// The whole diff stops at DiffMaxFiles, past the last generated file.
	all, err := r.DiffWithOptions("", second, DiffOptions{})
	is.NoErr(err)
	is.Equal(len(all.Files), DiffMaxFiles)
	last := fmt.Sprintf("gen/%04d.txt", DiffMaxFiles+4)
	is.True(!strings.Contains(strings.Join(diffNames(all), "\n"), last))

	// That file alone is still there.
	one, err := r.DiffWithOptions("", second, DiffOptions{Path: last})
	is.NoErr(err)
	is.Equal(diffNames(one), []string{last})

	// A rename keeps its old name.
	mv, err := r.DiffWithOptions("", second, DiffOptions{Path: "new/name.txt"})
	is.NoErr(err)
	is.Equal(len(mv.Files), 1)
	is.True(mv.Files[0].IsRenamed())
	is.Equal(mv.Files[0].OldName(), "old/name.txt")

	// So does a compare, given its base.
	cmp, err := r.DiffWithOptions(first, second, DiffOptions{Path: "new/name.txt"})
	is.NoErr(err)
	is.True(len(cmp.Files) == 1 && cmp.Files[0].IsRenamed())

	// A path is a path, never a pattern: as a glob, [ab].txt would match
	// a.txt too. (A name with : would do as well, but Windows can't make
	// one.)
	odd, err := r.DiffWithOptions("", second, DiffOptions{Path: "[ab].txt"})
	is.NoErr(err)
	is.Equal(diffNames(odd), []string{"[ab].txt"})

	// The first commit has no parent to diff against.
	root, err := r.DiffWithOptions("", first, DiffOptions{Path: "a.txt"})
	is.NoErr(err)
	is.Equal(diffNames(root), []string{"a.txt"})
	is.Equal(root.Files[0].NumAdditions(), 1)

	// A path the commit didn't touch has no changes.
	none, err := r.DiffWithOptions("", second, DiffOptions{Path: "nope.txt"})
	is.NoErr(err)
	is.Equal(len(none.Files), 0)
}
