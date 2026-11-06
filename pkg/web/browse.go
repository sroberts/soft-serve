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

	gitmodule "github.com/aymanbagabas/git-module"
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
	case errors.Is(err, errForbidden):
		return http.StatusForbidden
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
	// Access is the caller's: read-only, read-write or admin-access.
	Access string `json:"access"`
	// MirrorOf is where a mirror is copied from, without any credentials,
	// and MirrorSyncedAt when it last fetched from there.
	MirrorOf       string     `json:"mirror_of,omitempty"`
	MirrorSyncedAt *time.Time `json:"mirror_synced_at,omitempty"`
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
	// Date is when a branch's tip was committed, or a tag made.
	Date    time.Time `json:"date"`
	Summary string    `json:"summary"` // of the commit
	// Annotated tags have a tagger and a message: release notes, often.
	Annotated bool       `json:"annotated,omitempty"`
	Tagger    *Signature `json:"tagger,omitempty"`
	Message   string     `json:"message,omitempty"`
	// Ahead and Behind count a branch's commits that the default branch
	// doesn't have, and the other way round. Left out for the default
	// branch, for tags, and when there wasn't time to count.
	Ahead  *int64 `json:"ahead,omitempty"`
	Behind *int64 `json:"behind,omitempty"`
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
	// Signature is how the commit was signed, if it was. Only a single
	// commit has it, never a list.
	Signature *CommitSignature `json:"signature,omitempty"`
}

// FileChange is one file a commit touched.
type FileChange struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"` // added, deleted, renamed or modified
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
	// Truncated is set when the file's diff was cut short: past 1,000
	// lines, unless the diff was asked for that file alone.
	Truncated bool `json:"truncated,omitempty"`
}

// CommitDetail is a commit with what it changed.
type CommitDetail struct {
	CommitInfo
	Files          []FileChange `json:"files"`
	Patch          string       `json:"patch"`
	PatchTruncated bool         `json:"patch_truncated"`
	// Path and IgnoreWhitespace say how the diff was asked for: one file's
	// alone, and without changes in whitespace.
	Path             string `json:"path,omitempty"`
	IgnoreWhitespace bool   `json:"ignore_whitespace,omitempty"`
}

// CommitPage is one page of a history.
type CommitPage struct {
	Ref string `json:"ref"`
	// Path, when set, limits the history to commits that touched it;
	// Query to those whose message contains it, and Author to those whose
	// author's name or email does, both ignoring case.
	Path    string       `json:"path,omitempty"`
	Query   string       `json:"query,omitempty"`
	Author  string       `json:"author,omitempty"`
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
	// LastCommit is the latest commit to touch the entry, when asked for.
	LastCommit *CommitBrief `json:"last_commit,omitempty"`
}

// Tree is a directory at a revision.
type Tree struct {
	Ref     string      `json:"ref"`
	Commit  string      `json:"commit"`
	Path    string      `json:"path"`
	Entries []TreeEntry `json:"entries"`
	// LastCommit is the latest commit to touch the directory, when asked for.
	LastCommit *CommitBrief `json:"last_commit,omitempty"`
}

// Blob is a file at a revision.
type Blob struct {
	Ref       string `json:"ref"`
	Commit    string `json:"commit"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	// LFS is set for a file tracked by Git LFS: Size and Content are the
	// stored file's. LFSMissing means the server doesn't have the object,
	// so Content is the pointer.
	LFS        bool `json:"lfs"`
	LFSMissing bool `json:"lfs_missing,omitempty"`
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
	r, gr, _, err := b.openLevel(ctx, name)
	return r, gr, err
}

// openLevel is open, with the caller's access level.
func (b *browser) openLevel(ctx context.Context, name string) (proto.Repository, *git.Repository, access.AccessLevel, error) {
	name = utils.SanitizeRepo(name)
	if name == "" {
		return nil, nil, access.NoAccess, errNotFound
	}
	r, level := b.be.Readable(ctx, name, b.user)
	if r == nil {
		return nil, nil, access.NoAccess, errNotFound
	}
	gr, err := r.Open()
	if err != nil {
		return nil, nil, access.NoAccess, err
	}
	return r, gr, level, nil
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
		level := b.be.AccessLevelForUser(ctx, r.Name(), b.user)
		if r.IsHidden() || level < access.ReadOnlyAccess {
			continue
		}
		info := b.info(r, nil, level)
		if gr, err := r.Open(); err == nil {
			info = b.info(r, gr, level)
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b RepoInfo) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// repo describes one repository.
func (b *browser) repo(ctx context.Context, name string) (RepoInfo, error) {
	r, gr, level, err := b.openLevel(ctx, name)
	if err != nil {
		return RepoInfo{}, err
	}
	info := b.info(r, gr, level)
	if info.Mirror {
		info.MirrorOf, info.MirrorSyncedAt = mirrorDetails(ctx, gr)
	}
	return info, nil
}

func (b *browser) info(r proto.Repository, gr *git.Repository, level access.AccessLevel) RepoInfo {
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
		Access:      level.String(),
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

// commitFilter narrows a history.
type commitFilter struct {
	Path   string // commits that touched it
	Query  string // in the message
	Author string // in the author's name or email
}

func (b *browser) commits(ctx context.Context, name, rev string, f commitFilter, page, perPage int) (CommitPage, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return CommitPage{}, err
	}
	rev, c, err := resolve(gr, rev)
	if err != nil {
		return CommitPage{}, err
	}
	p, err := cleanPath(f.Path)
	if err != nil {
		return CommitPage{}, err
	}
	f.Path = p
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defaultPerPage
	}
	perPage = min(perPage, maxPerPage)
	if f.Query != "" || f.Author != "" {
		return b.searchCommits(ctx, gr, rev, c.ID.String(), f, page, perPage)
	}
	sha := c.ID.String()
	cs, err := gr.Repository.CommitsByPage(sha, page, perPage, gitmodule.CommitsByPageOptions{Path: p})
	if err != nil {
		return CommitPage{}, err
	}
	total, err := gr.RevListCount([]string{sha}, gitmodule.RevListCountOptions{Path: p})
	if err != nil {
		return CommitPage{}, err
	}
	out := CommitPage{Ref: rev, Path: p, Page: page, PerPage: perPage, Total: total, Commits: make([]CommitInfo, 0, len(cs))}
	for _, c := range cs {
		out.Commits = append(out.Commits, commitInfo(c))
	}
	return out, nil
}

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// diffOpts say how to show a diff.
type diffOpts struct {
	IgnoreWhitespace bool
	// Path shows only the file at it (its old or new name), and in full.
	Path string
}

func (b *browser) commit(ctx context.Context, name, sha string, o diffOpts) (CommitDetail, error) {
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
	out := CommitDetail{CommitInfo: commitInfo(c), Files: []FileChange{}, IgnoreWhitespace: o.IgnoreWhitespace}
	if out.Path, err = cleanPath(o.Path); err != nil {
		return CommitDetail{}, err
	}
	diff, err := gr.DiffWithOptions("", out.SHA, gitDiffOptions(o, out.Path))
	if err != nil {
		return CommitDetail{}, err
	}
	out.Files, out.Patch, out.PatchTruncated = changes(diff, out.Path)
	out.Signature = b.commitSignature(ctx, gr, out.SHA)
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

// tree lists a directory; withLast adds each entry's latest commit.
func (b *browser) tree(ctx context.Context, name, rev, p string, withLast bool) (Tree, error) {
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
	if withLast {
		if err := lastCommits(ctx, gr, out.Commit, &out); err != nil {
			return Tree{}, err
		}
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
	repo, gr, err := b.open(ctx, name)
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
	if lf := b.lfsLookup(repo, te); lf != nil {
		out.LFS = true
		out.Size = lf.pointer.Size
		if lf.object == nil {
			out.LFSMissing = true
			out.Content = lf.pointer.String()
			return out, nil
		}
		defer lf.object.Close() //nolint:errcheck
		rd, bin := sniff(lf.object)
		if bin {
			out.Binary = true
			return out, nil
		}
		w := &limitWriter{n: maxBlobBytes}
		if _, err := io.Copy(w, rd); err != nil {
			return Blob{}, err
		}
		out.Content, out.Truncated = finishText(w)
		return out, nil
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
	out.Content, out.Truncated = finishText(w)
	return out, nil
}

// finishText is the text a limitWriter kept, and whether it was cut short.
func finishText(w *limitWriter) (string, bool) {
	if w.total > int64(len(w.buf)) {
		return truncateUTF8(string(w.buf), len(w.buf)), true
	}
	return string(w.buf), false
}

// raw streams a file's bytes to w and returns its path.
func (b *browser) raw(ctx context.Context, name, rev, p string, w io.Writer, before func(path string, size int64, binary bool)) error {
	repo, gr, err := b.open(ctx, name)
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
	clean, _ := cleanPath(p)
	if lf := b.lfsLookup(repo, te); lf != nil && lf.object != nil {
		defer lf.object.Close() //nolint:errcheck
		rd, bin := sniff(lf.object)
		before(clean, lf.pointer.Size, bin)
		_, err := io.Copy(w, rd)
		return err
	}
	f := te.File()
	bin, _ := f.IsBinary()
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
