package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	gopath "path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/soft-serve/git"
	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/utils"
)

// The read-only view of repositories that the web pages, the REST API and
// the MCP tools share. Everything here checks the caller's access the way git
// over HTTP does: a repository the caller may not read answers exactly like
// one that does not exist.

const (
	// maxBlobBytes is how much of a file the API, the MCP tools and the file
	// page return inline. The raw endpoint streams the whole file.
	maxBlobBytes = 1 << 20
	// maxPatchBytes caps the patch a commit returns inline.
	maxPatchBytes = 1 << 20
	// maxPerPage caps a page of commits.
	maxPerPage = 100
	// defaultPerPage is a page of commits when the caller does not say.
	defaultPerPage = 30
)

var (
	errNotFound   = errors.New("not found")
	errBadRequest = errors.New("bad request")
	errEmptyRepo  = errors.New("repository is empty")
	errIsDir      = errors.New("path is a directory")
	errNotDir     = errors.New("path is not a directory")
)

// badRequest wraps errBadRequest with what was wrong.
func badRequest(format string, v ...any) error {
	return fmt.Errorf("%w: %s", errBadRequest, fmt.Sprintf(format, v...))
}

// statusFor maps a browse error to an HTTP status.
func statusFor(err error) int {
	switch {
	case errors.Is(err, errNotFound), errors.Is(err, errEmptyRepo):
		return http.StatusNotFound
	case errors.Is(err, errBadRequest), errors.Is(err, errIsDir), errors.Is(err, errNotDir):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// publicMessage is what a caller is told about err. Internal errors can carry
// server paths, so they are reported generically.
func publicMessage(err error) string {
	if statusFor(err) == http.StatusInternalServerError {
		return "internal error"
	}
	return err.Error()
}

// browser answers read-only questions about repositories for one caller.
type browser struct {
	be   *backend.Backend
	cfg  *config.Config
	user proto.User // nil for an anonymous caller
	// base is the scheme and host the caller reached us at, for HTTPS clone
	// URLs. Built from the request, never configured (no hard-coded host).
	base string
}

func newBrowser(ctx context.Context, user proto.User, base string) *browser {
	return &browser{
		be:   backend.FromContext(ctx),
		cfg:  config.FromContext(ctx),
		user: user,
		base: base,
	}
}

// RepoInfo describes a repository.
type RepoInfo struct {
	Name          string    `json:"name"`
	ProjectName   string    `json:"project_name,omitempty"`
	Description   string    `json:"description,omitempty"`
	Private       bool      `json:"private"`
	Mirror        bool      `json:"mirror"`
	Hidden        bool      `json:"hidden"`
	Empty         bool      `json:"empty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
	Clone         CloneURLs `json:"clone"`
}

// CloneURLs are the ways to clone a repository.
type CloneURLs struct {
	SSH   string `json:"ssh,omitempty"`
	HTTPS string `json:"https,omitempty"`
}

// RefInfo is a branch or a tag.
type RefInfo struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

// Refs are a repository's branches and tags.
type Refs struct {
	DefaultBranch string    `json:"default_branch,omitempty"`
	Branches      []RefInfo `json:"branches"`
	Tags          []RefInfo `json:"tags"`
}

// Signature is who made a commit, and when.
type Signature struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	When  time.Time `json:"when"`
}

// CommitInfo describes a commit.
type CommitInfo struct {
	SHA       string    `json:"sha"`
	ShortSHA  string    `json:"short_sha"`
	Summary   string    `json:"summary"`
	Message   string    `json:"message"`
	Author    Signature `json:"author"`
	Committer Signature `json:"committer"`
	Parents   []string  `json:"parents"`
}

// FileChange is one file a commit touched.
type FileChange struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"` // added, deleted, renamed or modified
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
}

// CommitDetail is a commit with what it changed.
type CommitDetail struct {
	CommitInfo
	Files          []FileChange `json:"files"`
	Patch          string       `json:"patch"`
	PatchTruncated bool         `json:"patch_truncated"`
}

// CommitPage is one page of a history.
type CommitPage struct {
	Ref     string       `json:"ref"`
	Page    int          `json:"page"`
	PerPage int          `json:"per_page"`
	Total   int64        `json:"total"`
	Commits []CommitInfo `json:"commits"`
}

// TreeEntry is one entry in a directory.
type TreeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"` // dir, file, symlink or submodule
	Size int64  `json:"size"`
	Mode string `json:"mode"`
}

// Tree is a directory at a revision.
type Tree struct {
	Ref     string      `json:"ref"`
	Commit  string      `json:"commit"`
	Path    string      `json:"path"`
	Entries []TreeEntry `json:"entries"`
}

// Blob is a file at a revision.
type Blob struct {
	Ref       string `json:"ref"`
	Commit    string `json:"commit"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	// Content is the file's text; empty for a binary file.
	Content string `json:"content"`
}

// Readme is a repository's README at a revision.
type Readme struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// open returns a repository the caller may read, or errNotFound.
func (b *browser) open(ctx context.Context, name string) (proto.Repository, *git.Repository, error) {
	name = utils.SanitizeRepo(name)
	if name == "" {
		return nil, nil, errNotFound
	}
	r, _ := b.be.Readable(ctx, name, b.user)
	if r == nil {
		return nil, nil, errNotFound
	}
	gr, err := r.Open()
	if err != nil {
		return nil, nil, err
	}
	return r, gr, nil
}

// repos lists the repositories the caller may read, by name. Hidden
// repositories are left out, as they are in the TUI; they can still be
// opened by name.
func (b *browser) repos(ctx context.Context) ([]RepoInfo, error) {
	all, err := b.be.Repositories(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RepoInfo, 0, len(all))
	for _, r := range all {
		if r.IsHidden() || b.be.AccessLevelForUser(ctx, r.Name(), b.user) < access.ReadOnlyAccess {
			continue
		}
		info := b.info(r, nil)
		if gr, err := r.Open(); err == nil {
			info = b.info(r, gr)
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b RepoInfo) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// repo describes one repository.
func (b *browser) repo(ctx context.Context, name string) (RepoInfo, error) {
	r, gr, err := b.open(ctx, name)
	if err != nil {
		return RepoInfo{}, err
	}
	return b.info(r, gr), nil
}

func (b *browser) info(r proto.Repository, gr *git.Repository) RepoInfo {
	info := RepoInfo{
		Name:        r.Name(),
		ProjectName: r.ProjectName(),
		Description: r.Description(),
		Private:     r.IsPrivate(),
		Mirror:      r.IsMirror(),
		Hidden:      r.IsHidden(),
		UpdatedAt:   r.UpdatedAt(),
		Clone:       b.cloneURLs(r.Name()),
		Empty:       true,
	}
	if gr != nil {
		if head, err := gr.HEAD(); err == nil {
			info.DefaultBranch = head.Name().Short()
			info.Empty = false
		}
	}
	return info
}

func (b *browser) cloneURLs(name string) CloneURLs {
	var c CloneURLs
	if b.cfg.SSH.Enabled && b.cfg.SSH.PublicURL != "" {
		c.SSH = b.cfg.SSH.PublicURL + "/" + name
	}
	if b.base != "" {
		c.HTTPS = b.base + "/" + name + ".git"
	}
	return c
}

// revPattern is what a revision may look like: a ref name or a hash, never
// anything git could read as an option.
var revPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/@{}^~+-]*$`)

// resolve turns a revision (branch, tag or hash; empty for the default
// branch) into its commit.
func resolve(gr *git.Repository, rev string) (string, *git.Commit, error) {
	if rev == "" {
		head, err := gr.HEAD()
		if err != nil {
			return "", nil, errEmptyRepo
		}
		rev = head.Name().Short()
	}
	if len(rev) > 256 || !revPattern.MatchString(rev) || strings.Contains(rev, "..") {
		return "", nil, badRequest("invalid ref %q", rev)
	}
	c, err := gr.CatFileCommit(rev)
	if err != nil {
		return "", nil, fmt.Errorf("%w: ref %q", errNotFound, rev)
	}
	return rev, c, nil
}

// cleanPath normalises a path inside a repository: forward slashes, no
// leading slash, nothing that climbs out.
func cleanPath(p string) (string, error) {
	p = strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
	if p == "" {
		return "", nil
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || strings.ContainsAny(seg, "\x00\n\r") {
			return "", badRequest("invalid path %q", p)
		}
	}
	p = gopath.Clean(p)
	if p == "." {
		return "", nil
	}
	return p, nil
}

func (b *browser) refs(ctx context.Context, name string) (Refs, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return Refs{}, err
	}
	refs, err := gr.References()
	if err != nil {
		return Refs{}, err
	}
	out := Refs{Branches: []RefInfo{}, Tags: []RefInfo{}}
	if head, err := gr.HEAD(); err == nil {
		out.DefaultBranch = head.Name().Short()
	}
	for _, r := range refs {
		ri := RefInfo{Name: r.Name().Short(), Commit: r.ID}
		switch {
		case r.IsBranch():
			out.Branches = append(out.Branches, ri)
		case r.IsTag():
			out.Tags = append(out.Tags, ri)
		}
	}
	slices.SortFunc(out.Branches, func(a, c RefInfo) int {
		switch {
		case a.Name == out.DefaultBranch:
			return -1
		case c.Name == out.DefaultBranch:
			return 1
		}
		return strings.Compare(a.Name, c.Name)
	})
	slices.SortFunc(out.Tags, func(a, c RefInfo) int { return strings.Compare(c.Name, a.Name) })
	return out, nil
}

func commitInfo(c *git.Commit) CommitInfo {
	sha := c.ID.String()
	info := CommitInfo{
		SHA:      sha,
		ShortSHA: sha[:min(7, len(sha))],
		Summary:  c.Summary(),
		Message:  c.Message,
		Parents:  []string{},
	}
	if c.Author != nil {
		info.Author = Signature{Name: c.Author.Name, Email: c.Author.Email, When: c.Author.When}
	}
	if c.Committer != nil {
		info.Committer = Signature{Name: c.Committer.Name, Email: c.Committer.Email, When: c.Committer.When}
	}
	for i := range c.ParentsCount() {
		if id, err := c.ParentID(i); err == nil {
			info.Parents = append(info.Parents, id.String())
		}
	}
	return info
}

func (b *browser) commits(ctx context.Context, name, rev string, page, perPage int) (CommitPage, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return CommitPage{}, err
	}
	rev, c, err := resolve(gr, rev)
	if err != nil {
		return CommitPage{}, err
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defaultPerPage
	}
	perPage = min(perPage, maxPerPage)
	sha := c.ID.String()
	cs, err := gr.Repository.CommitsByPage(sha, page, perPage)
	if err != nil {
		return CommitPage{}, err
	}
	total, err := gr.RevListCount([]string{sha})
	if err != nil {
		return CommitPage{}, err
	}
	out := CommitPage{Ref: rev, Page: page, PerPage: perPage, Total: total, Commits: make([]CommitInfo, 0, len(cs))}
	for _, c := range cs {
		out.Commits = append(out.Commits, commitInfo(c))
	}
	return out, nil
}

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

func (b *browser) commit(ctx context.Context, name, sha string) (CommitDetail, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return CommitDetail{}, err
	}
	if !shaPattern.MatchString(sha) {
		return CommitDetail{}, badRequest("invalid commit %q", sha)
	}
	c, err := gr.CatFileCommit(sha)
	if err != nil {
		return CommitDetail{}, fmt.Errorf("%w: commit %q", errNotFound, sha)
	}
	out := CommitDetail{CommitInfo: commitInfo(c), Files: []FileChange{}}
	diff, err := gr.Diff(c)
	if err != nil {
		return CommitDetail{}, err
	}
	for _, f := range diff.Files {
		fc := FileChange{
			Path:      f.Name,
			Status:    "modified",
			Additions: f.NumAdditions(),
			Deletions: f.NumDeletions(),
			Binary:    f.IsBinary(),
		}
		switch {
		case f.IsCreated():
			fc.Status = "added"
		case f.IsDeleted():
			fc.Status = "deleted"
		case f.IsRenamed():
			fc.Status = "renamed"
			fc.OldPath = f.OldName()
		}
		out.Files = append(out.Files, fc)
	}
	out.Patch = diff.Patch()
	if len(out.Patch) > maxPatchBytes {
		out.Patch = truncateUTF8(out.Patch, maxPatchBytes)
		out.PatchTruncated = true
	}
	return out, nil
}

// entryType names a tree entry's kind.
func entryType(e *git.TreeEntry) string {
	switch {
	case e.IsTree():
		return "dir"
	case e.IsCommit():
		return "submodule"
	case e.IsSymlink():
		return "symlink"
	}
	return "file"
}

// lookup finds path at rev: the tree for a directory, the entry for anything
// else.
func lookup(gr *git.Repository, rev, p string) (string, *git.Commit, *git.Tree, *git.TreeEntry, error) {
	rev, c, err := resolve(gr, rev)
	if err != nil {
		return "", nil, nil, nil, err
	}
	p, err = cleanPath(p)
	if err != nil {
		return "", nil, nil, nil, err
	}

	tree, err := gr.LsTree(c.ID.String())
	if err != nil {
		return "", nil, nil, nil, err
	}
	if p == "" {
		return rev, c, tree, nil, nil
	}
	te, err := tree.TreeEntry(p)
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("%w: path %q", errNotFound, p)
	}
	if te.IsTree() {
		sub, err := tree.SubTree(p)
		if err != nil {
			return "", nil, nil, nil, err
		}
		return rev, c, sub, nil, nil
	}
	return rev, c, nil, te, nil
}

func (b *browser) tree(ctx context.Context, name, rev, p string) (Tree, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return Tree{}, err
	}
	rev, c, tree, _, err := lookup(gr, rev, p)
	if err != nil {
		return Tree{}, err
	}
	if tree == nil {
		return Tree{}, errNotDir
	}
	ents, err := tree.Entries()
	if err != nil {
		return Tree{}, err
	}
	ents.Sort()
	out := Tree{Ref: rev, Commit: c.ID.String(), Path: tree.Path, Entries: make([]TreeEntry, 0, len(ents))}
	for _, e := range ents {
		out.Entries = append(out.Entries, TreeEntry{
			Name: e.Name(),
			Path: gopath.Join(tree.Path, e.Name()),
			Type: entryType(e),
			Size: e.Size(),
			Mode: e.Mode().String(),
		})
	}
	return out, nil
}

// limitWriter keeps the first n bytes written to it and counts the rest.
type limitWriter struct {
	buf   []byte
	n     int
	total int64
}

func (w *limitWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	if room := w.n - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *browser) blob(ctx context.Context, name, rev, p string) (Blob, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return Blob{}, err
	}
	rev, c, tree, te, err := lookup(gr, rev, p)
	if err != nil {
		return Blob{}, err
	}
	if tree != nil {
		return Blob{}, errIsDir
	}
	clean, _ := cleanPath(p)
	out := Blob{Ref: rev, Commit: c.ID.String(), Path: clean, Size: te.Size()}
	if te.IsCommit() {
		return Blob{}, badRequest("%q is a submodule", clean)
	}
	f := te.File()
	if bin, err := f.IsBinary(); err == nil && bin {
		out.Binary = true
		return out, nil
	}
	w := &limitWriter{n: maxBlobBytes}
	if err := f.Pipeline(w, io.Discard); err != nil {
		return Blob{}, err
	}
	out.Content = string(w.buf)
	if w.total > int64(len(w.buf)) {
		out.Content = truncateUTF8(out.Content, len(out.Content))
		out.Truncated = true
	}
	return out, nil
}

// raw streams a file's bytes to w and returns its path.
func (b *browser) raw(ctx context.Context, name, rev, p string, w io.Writer, before func(path string, size int64, binary bool)) error {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return err
	}
	_, _, tree, te, err := lookup(gr, rev, p)
	if err != nil {
		return err
	}
	if tree != nil {
		return errIsDir
	}
	if te.IsCommit() {
		return badRequest("%q is a submodule", p)
	}
	f := te.File()
	bin, _ := f.IsBinary()
	clean, _ := cleanPath(p)
	before(clean, te.Size(), bin)
	return f.Pipeline(w, io.Discard)
}

// readmePattern matches README, README.md, readme.rst and the like.
var readmePattern = regexp.MustCompile(`(?i)^readme(\.[a-z0-9]+)?$`)

// readme finds the README at rev: in the root, then .github, then docs, as
// the TUI does. It prefers Markdown when there is more than one.
func (b *browser) readme(ctx context.Context, name, rev string) (Readme, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return Readme{}, err
	}
	_, c, err := resolve(gr, rev)
	if err != nil {
		return Readme{}, err
	}
	root, err := gr.LsTree(c.ID.String())
	if err != nil {
		return Readme{}, err
	}
	for _, dir := range []string{"", ".github", "docs"} {
		t := root
		if dir != "" {
			if t, err = root.SubTree(dir); err != nil {
				continue
			}
		}
		ents, err := t.Entries()
		if err != nil {
			continue
		}
		var pick *git.TreeEntry
		for _, e := range ents {
			if !e.IsBlob() || e.IsSymlink() || !readmePattern.MatchString(e.Name()) {
				continue
			}
			if pick == nil || isMarkdown(e.Name()) && !isMarkdown(pick.Name()) {
				pick = e
			}
		}
		if pick == nil {
			continue
		}
		w := &limitWriter{n: maxBlobBytes}
		if err := pick.File().Pipeline(w, io.Discard); err != nil {
			return Readme{}, err
		}
		return Readme{Path: gopath.Join(dir, pick.Name()), Content: truncateUTF8(string(w.buf), len(w.buf))}, nil
	}
	return Readme{}, fmt.Errorf("%w: no README", errNotFound)
}

func isMarkdown(name string) bool {
	switch strings.ToLower(gopath.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

// truncateUTF8 cuts s to at most n bytes, dropping a character the cut
// would split.
func truncateUTF8(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.ToValidUTF8(s, "")
}
