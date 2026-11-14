package git

import (
	"bytes"
	"fmt"
	"io"
	gopath "path"
	"path/filepath"
	"strings"

	"github.com/aymanbagabas/git-module"
)

var (
	// DiffMaxFile is the maximum number of files to show in a diff.
	DiffMaxFiles = 1000
	// DiffMaxFileLines is the maximum number of lines to show in a file diff.
	DiffMaxFileLines = 1000
	// DiffMaxLineChars is the maximum number of characters to show in a line diff.
	DiffMaxLineChars = 1000
)

// Repository is a wrapper around git.Repository with helper methods.
type Repository struct {
	*git.Repository
	Path   string
	IsBare bool
}

// Clone clones a repository.
func Clone(src, dst string, opts ...git.CloneOptions) error {
	return git.Clone(src, dst, opts...)
}

// Init initializes and opens a new git repository.
func Init(path string, bare bool) (*Repository, error) {
	if bare {
		path = strings.TrimSuffix(path, ".git") + ".git"
	}

	err := git.Init(path, git.InitOptions{Bare: bare})
	if err != nil {
		return nil, err
	}
	return Open(path)
}

func gitDir(r *git.Repository) (string, error) {
	return r.RevParse("--git-dir")
}

// Open opens a git repository at the given path.
func Open(path string) (*Repository, error) {
	repo, err := git.Open(path)
	if err != nil {
		return nil, err
	}
	gp, err := gitDir(repo)
	if err != nil || (gp != "." && gp != ".git") {
		return nil, ErrNotAGitRepository
	}
	return &Repository{
		Repository: repo,
		Path:       path,
		IsBare:     gp == ".",
	}, nil
}

// HEAD returns the HEAD reference for a repository.
func (r *Repository) HEAD() (*Reference, error) {
	rn, err := r.Repository.SymbolicRef(git.SymbolicRefOptions{Name: "HEAD"})
	if err != nil {
		return nil, err
	}
	hash, err := r.ShowRefVerify(rn)
	if err != nil {
		return nil, err
	}
	return &Reference{
		Reference: &git.Reference{
			ID:      hash,
			Refspec: rn,
		},
		path: r.Path,
	}, nil
}

// References returns the references for a repository.
func (r *Repository) References() ([]*Reference, error) {
	refs, err := r.ShowRef()
	if err != nil {
		return nil, err
	}
	rrefs := make([]*Reference, 0, len(refs))
	for _, ref := range refs {
		rrefs = append(rrefs, &Reference{
			Reference: ref,
			path:      r.Path,
		})
	}
	return rrefs, nil
}

// LsTree returns the tree for the given reference.
func (r *Repository) LsTree(ref string) (*Tree, error) {
	tree, err := r.Repository.LsTree(ref)
	if err != nil {
		return nil, err
	}
	return &Tree{
		Tree:       tree,
		Path:       "",
		Repository: r,
	}, nil
}

// Tree returns the tree for the given reference.
func (r *Repository) Tree(ref *Reference) (*Tree, error) {
	if ref == nil {
		rref, err := r.HEAD()
		if err != nil {
			return nil, err
		}
		ref = rref
	}
	return r.LsTree(ref.ID)
}

// TreePath returns the tree for the given path.
// Git paths always use forward slashes regardless of OS.
func (r *Repository) TreePath(ref *Reference, path string) (*Tree, error) {
	path = gopath.Clean(filepath.ToSlash(path))
	if path == "." {
		path = ""
	}
	if path == "" {
		return r.Tree(ref)
	}
	t, err := r.Tree(ref)
	if err != nil {
		return nil, err
	}
	return t.SubTree(path)
}

// Diff returns the diff for the given commit.
func (r *Repository) Diff(commit *Commit) (*Diff, error) {
	diff, err := r.Repository.Diff(commit.ID.String(), DiffMaxFiles, DiffMaxFileLines, DiffMaxLineChars, git.DiffOptions{
		CommandOptions: git.CommandOptions{
			Envs: []string{"GIT_CONFIG_GLOBAL=/dev/null"},
		},
	})
	if err != nil {
		return nil, err
	}
	return toDiff(diff), nil
}

// DiffOptions change how DiffWithOptions computes a diff.
type DiffOptions struct {
	// IgnoreWhitespace leaves out changes in whitespace, as git diff -w.
	IgnoreWhitespace bool
	// MaxFileLines caps the lines parsed for each file; DiffMaxFileLines
	// when 0.
	MaxFileLines int
	// Path limits the diff to one file, as it's named at head. A file
	// renamed to Path keeps its old name, so the rename still shows. Git
	// only reads the files it needs, however large the rest of the diff.
	Path string
}

// literalPaths makes git read a pathspec as a plain path, never as magic
// such as :(exclude).
const literalPaths = "GIT_LITERAL_PATHSPECS=1"

// DiffWithOptions returns the changes from base to head, or, with base
// empty, the changes the commit head made.
func (r *Repository) DiffWithOptions(base, head string, opts DiffOptions) (*Diff, error) {
	maxLines := opts.MaxFileLines
	if maxLines <= 0 {
		maxLines = DiffMaxFileLines
	}
	o := git.DiffOptions{
		Base: base,
		CommandOptions: git.CommandOptions{
			Envs: []string{"GIT_CONFIG_GLOBAL=/dev/null"},
		},
	}
	if opts.IgnoreWhitespace {
		o.Args = []string{"--ignore-all-space"}
	}
	var d *Diff
	if opts.Path != "" {
		pd, err := r.pathDiff(head, opts.Path, maxLines, o)
		if err != nil {
			return nil, err
		}
		d = pd
	} else {
		diff, err := r.Repository.Diff(head, DiffMaxFiles, maxLines, DiffMaxLineChars, o)
		if err != nil {
			return nil, err
		}
		d = toDiff(diff)
	}
	d.capLines(maxLines)
	return d, nil
}

// pathDiff is Repository.Diff for one path: the same git commands, with
// the path, and its old name when it was renamed, as pathspecs.
func (r *Repository) pathDiff(head, path string, maxLines int, o git.DiffOptions) (*Diff, error) {
	o.Envs = append(o.Envs, literalPaths)
	base := o.Base
	if base == "" {
		c, err := r.CatFileCommit(head, git.CatFileCommitOptions{Timeout: o.Timeout})
		if err != nil {
			return nil, err
		}
		if c.ParentsCount() > 0 {
			p, err := c.ParentID(0)
			if err != nil {
				return nil, err
			}
			base = p.String()
		}
	}

	var cmd *git.Command
	if base == "" {
		// The first commit: nothing was renamed.
		cmd = git.NewCommand("show").AddOptions(o.CommandOptions).
			AddArgs("--full-index", "--end-of-options", head, "--", path)
	} else {
		paths, err := r.renamedFrom(base, head, path, o)
		if err != nil {
			return nil, err
		}
		cmd = git.NewCommand("diff").AddOptions(o.CommandOptions).
			AddArgs(append([]string{"--full-index", "-M", base, "--end-of-options", head, "--"}, paths...)...)
	}

	stdout, w := io.Pipe()
	done := make(chan git.SteamParseDiffResult)
	go git.StreamParseDiff(stdout, done, DiffMaxFiles, maxLines, DiffMaxLineChars)
	stderr := new(bytes.Buffer)
	err := cmd.RunInDirPipelineWithTimeout(o.Timeout, w, stderr, r.Path)
	_ = w.Close()
	result := <-done
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if result.Err != nil {
		return nil, result.Err
	}
	return toDiff(result.Diff), nil
}

// renamedFrom is path, and the name it had at base if it was renamed to
// path between base and head. Finding a rename reads only the names that
// changed, not their content.
func (r *Repository) renamedFrom(base, head, path string, o git.DiffOptions) ([]string, error) {
	out, err := git.NewCommand("diff").AddOptions(git.CommandOptions{Envs: o.Envs}).
		AddArgs("--name-status", "-z", "-M", "--diff-filter=R", base, "--end-of-options", head).
		RunInDirWithTimeout(o.Timeout, r.Path)
	if err != nil {
		return nil, err
	}
	// Each rename is "R<score>\0old\0new\0".
	f := strings.Split(string(out), "\x00")
	for i := 0; i+2 < len(f); i += 3 {
		if f[i+2] == path {
			return []string{f[i+1], path}, nil
		}
	}
	return []string{path}, nil
}

// Patch returns the patch for the given reference.
func (r *Repository) Patch(commit *Commit) (string, error) {
	diff, err := r.Diff(commit)
	if err != nil {
		return "", err
	}
	return diff.Patch(), err
}

// CountCommits returns the number of commits in the repository.
func (r *Repository) CountCommits(ref *Reference) (int64, error) {
	return r.RevListCount([]string{ref.Name().String()})
}

// CommitsByPage returns the commits for a given page and size.
func (r *Repository) CommitsByPage(ref *Reference, page, size int) (Commits, error) {
	cs, err := r.Repository.CommitsByPage(ref.Name().String(), page, size)
	if err != nil {
		return nil, err
	}
	commits := make(Commits, len(cs))
	copy(commits, cs)
	return commits, nil
}

// SymbolicRef returns or updates the symbolic reference for the given name.
// Both name and ref can be empty.
func (r *Repository) SymbolicRef(name string, ref string, opts ...git.SymbolicRefOptions) (string, error) {
	var opt git.SymbolicRefOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	opt.Name = name
	opt.Ref = ref
	return r.Repository.SymbolicRef(opt)
}
