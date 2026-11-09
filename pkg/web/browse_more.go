package web

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	gopath "path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	gitmodule "github.com/aymanbagabas/git-module"
	"github.com/charmbracelet/soft-serve/git"
	"github.com/charmbracelet/soft-serve/pkg/access"
)

const (
	// lastCommitScan and lastCommitBudget bound the history walked to find
	// each entry's latest commit. Entries not reached are left without one.
	lastCommitScan   = 5000
	lastCommitBudget = 3 * time.Second
	// maxCompareCommits caps the commits a comparison lists.
	maxCompareCommits = 250
	// Search limits: matches in all, matches shown per file, and the
	// longest line returned.
	maxSearchMatches = 200
	maxFileMatches   = 5
	maxMatchBytes    = 300
	// archiveTimeout bounds building one archive.
	archiveTimeout = 10 * time.Minute
)

var errForbidden = errors.New("forbidden")

// --- Latest commit per entry ---

// lastCommits fills in the latest commit of a directory and of each of its
// entries, in one walk of the history at sha.
func lastCommits(ctx context.Context, gr *git.Repository, sha string, t *Tree) error {
	want := make(map[string]int, len(t.Entries))
	for i, e := range t.Entries {
		want[e.Name] = i
	}
	left := len(want)
	ctx, cancel := context.WithTimeout(ctx, lastCommitBudget)
	defer cancel()

	args := []string{
		"-c", "core.quotePath=false", "--literal-pathspecs", "log",
		"--format=%x1e%H%x1f%at%x1f%an%x1f%s", "--name-only", "--no-renames",
		sha, "--",
	}
	if t.Path != "" {
		args = append(args, t.Path)
	}
	var cur *CommitBrief
	seen := 0
	err := scanGit(ctx, gr.Path, func(line string) error {
		if rec, ok := strings.CutPrefix(line, "\x1e"); ok {
			f := strings.SplitN(rec, "\x1f", 4)
			if len(f) < 4 {
				return nil
			}
			c := newBrief(f[0], f[1], f[2], f[3])
			cur = &c
			if t.LastCommit == nil {
				t.LastCommit = cur
			}
			if seen++; seen > lastCommitScan || left == 0 {
				return errStopScan
			}
			return nil
		}
		if line == "" || cur == nil {
			return nil
		}
		rel := line
		if t.Path != "" {
			rel = strings.TrimPrefix(line, t.Path+"/")
		}
		top, _, _ := strings.Cut(rel, "/")
		if i, ok := want[top]; ok && t.Entries[i].LastCommit == nil {
			t.Entries[i].LastCommit = cur
			if left--; left == 0 {
				return errStopScan
			}
		}
		return nil
	}, args...)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// Out of time: what was found is still right.
		return nil
	}
	return err
}

// --- Branches and tags, with dates ---

// refFormat is one ref per record: name, type, object, peeled object, date,
// subject, peeled subject, tagger name and email, and the message body.
const refFormat = "%(refname)%1f%(objecttype)%1f%(objectname)%1f%(*objectname)%1f%(creatordate:unix)%1f" +
	"%(subject)%1f%(*subject)%1f%(taggername)%1f%(taggeremail)%1f%(contents:body)%1e"

var signatureStart = regexp.MustCompile(`(?m)^-----BEGIN (PGP|SSH) SIGNATURE-----`)

func (b *browser) refs(ctx context.Context, name string) (Refs, error) {
	return b.listRefs(ctx, name, true)
}

// listRefs is refs, counting how far each branch is from the default one
// only when counts is set: that takes a rev-list per branch.
func (b *browser) listRefs(ctx context.Context, name string, counts bool) (Refs, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return Refs{}, err
	}
	out := Refs{Branches: []RefInfo{}, Tags: []RefInfo{}}
	if head, err := gr.HEAD(); err == nil {
		out.DefaultBranch = head.Name().Short()
	}
	data, err := gitOutput(ctx, gr.Path, "for-each-ref", "--sort=-creatordate", "--format="+refFormat, "refs/heads", "refs/tags")
	if err != nil {
		return Refs{}, err
	}
	for _, rec := range strings.Split(string(data), "\x1e") {
		f := strings.Split(strings.TrimLeft(rec, "\n"), "\x1f")
		if len(f) < 10 {
			continue
		}
		ri := RefInfo{Commit: f[2], Summary: f[5], Date: newBrief("", f[4], "", "").When}
		if f[1] == "tag" {
			ri.Commit = f[3]
			ri.Summary = f[6]
			ri.Annotated = true
			msg := f[5] + "\n\n" + f[9]
			if loc := signatureStart.FindStringIndex(msg); loc != nil {
				msg = msg[:loc[0]]
			}
			ri.Message = strings.TrimSpace(msg)
			if f[7] != "" {
				ri.Tagger = &Signature{Name: f[7], Email: strings.Trim(f[8], "<>"), When: ri.Date}
			}
		}
		switch {
		case strings.HasPrefix(f[0], "refs/heads/"):
			ri.Name = strings.TrimPrefix(f[0], "refs/heads/")
			out.Branches = append(out.Branches, ri)
		case strings.HasPrefix(f[0], "refs/tags/"):
			ri.Name = strings.TrimPrefix(f[0], "refs/tags/")
			out.Tags = append(out.Tags, ri)
		}
	}
	if counts {
		aheadBehind(ctx, gr, &out)
	}
	// The default branch first, then the most recently updated.
	slices.SortStableFunc(out.Branches, func(a, c RefInfo) int {
		switch {
		case a.Name == out.DefaultBranch:
			return -1
		case c.Name == out.DefaultBranch:
			return 1
		}
		return 0
	})
	return out, nil
}

// --- Blame ---

// BlameHunk is a run of lines last changed by one commit.
type BlameHunk struct {
	Commit string   `json:"commit"`
	Start  int      `json:"start"` // line number of the first line, from 1
	Lines  []string `json:"lines"`
}

// Blame is who last changed each line of a file.
type Blame struct {
	Ref     string                 `json:"ref"`
	Commit  string                 `json:"commit"`
	Path    string                 `json:"path"`
	Commits map[string]CommitBrief `json:"commits"`
	Hunks   []BlameHunk            `json:"hunks"`
}

var blameHeader = regexp.MustCompile(`^([0-9a-f]{40,64}) \d+ (\d+)(?: \d+)?$`)

func (b *browser) blame(ctx context.Context, name, rev, p string) (Blame, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return Blame{}, err
	}
	rev, c, tree, te, err := lookup(gr, rev, p)
	if err != nil {
		return Blame{}, err
	}
	if tree != nil {
		return Blame{}, errIsDir
	}
	clean, _ := cleanPath(p)
	if te.IsCommit() {
		return Blame{}, badRequest("%q is a submodule", clean)
	}
	if te.Size() > maxBlobBytes {
		return Blame{}, badRequest("%q is too large to blame", clean)
	}
	if bin, err := te.File().IsBinary(); err == nil && bin {
		return Blame{}, badRequest("%q is a binary file", clean)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := Blame{Ref: rev, Commit: c.ID.String(), Path: clean, Commits: map[string]CommitBrief{}, Hunks: []BlameHunk{}}
	meta := map[string]*[3]string{} // author, author-time, summary
	var cur string
	var line int
	err = scanGit(ctx, gr.Path, func(s string) error {
		if text, ok := strings.CutPrefix(s, "\t"); ok {
			if n := len(out.Hunks); n > 0 && out.Hunks[n-1].Commit == cur && out.Hunks[n-1].Start+len(out.Hunks[n-1].Lines) == line {
				out.Hunks[n-1].Lines = append(out.Hunks[n-1].Lines, text)
			} else {
				out.Hunks = append(out.Hunks, BlameHunk{Commit: cur, Start: line, Lines: []string{text}})
			}
			return nil
		}
		if m := blameHeader.FindStringSubmatch(s); m != nil {
			cur = m[1]
			line, _ = strconv.Atoi(m[2])
			if meta[cur] == nil {
				meta[cur] = &[3]string{}
			}
			return nil
		}
		key, val, _ := strings.Cut(s, " ")
		if m := meta[cur]; m != nil {
			switch key {
			case "author":
				m[0] = val
			case "author-time":
				m[1] = val
			case "summary":
				m[2] = val
			}
		}
		return nil
	}, "blame", "--porcelain", out.Commit, "--", clean)
	if err != nil {
		return Blame{}, err
	}
	for sha, m := range meta {
		out.Commits[sha] = newBrief(sha, m[1], m[0], m[2])
	}
	return out, nil
}

// --- Compare ---

// Compare is what head has that base doesn't: the commits since they
// diverged, and the changes from their merge base to head.
type Compare struct {
	Base             string       `json:"base"`
	Head             string       `json:"head"`
	BaseCommit       string       `json:"base_commit"`
	HeadCommit       string       `json:"head_commit"`
	MergeBase        string       `json:"merge_base"`
	TotalCommits     int64        `json:"total_commits"`
	Commits          []CommitInfo `json:"commits"`
	CommitsTruncated bool         `json:"commits_truncated"`
	Files            []FileChange `json:"files"`
	Patch            string       `json:"patch"`
	PatchTruncated   bool         `json:"patch_truncated"`
	// Path and IgnoreWhitespace say how the diff was asked for, as for a
	// commit.
	Path             string `json:"path,omitempty"`
	IgnoreWhitespace bool   `json:"ignore_whitespace,omitempty"`
}

func (b *browser) compare(ctx context.Context, name, base, head string, o diffOpts) (Compare, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return Compare{}, err
	}
	if head == "" {
		return Compare{}, badRequest("head is required")
	}
	base, bc, err := resolve(gr, base)
	if err != nil {
		return Compare{}, err
	}
	head, hc, err := resolve(gr, head)
	if err != nil {
		return Compare{}, err
	}
	out := Compare{Base: base, Head: head, BaseCommit: bc.ID.String(), HeadCommit: hc.ID.String(), Commits: []CommitInfo{}, Files: []FileChange{}, IgnoreWhitespace: o.IgnoreWhitespace}
	if out.Path, err = cleanPath(o.Path); err != nil {
		return Compare{}, err
	}
	mb, err := gitOutput(ctx, gr.Path, "merge-base", out.BaseCommit, out.HeadCommit)
	if exitCode(err) == 1 {
		return Compare{}, badRequest("%s and %s have no history in common", base, head)
	}
	if err != nil {
		return Compare{}, err
	}
	out.MergeBase = strings.TrimSpace(string(mb))
	if out.MergeBase == out.HeadCommit {
		return out, nil // head is already in base
	}

	span := out.MergeBase + ".." + out.HeadCommit
	if out.TotalCommits, err = gr.RevListCount([]string{span}); err != nil {
		return Compare{}, err
	}
	cs, err := gr.Repository.Log(span, gitmodule.LogOptions{MaxCount: maxCompareCommits})
	if err != nil {
		return Compare{}, err
	}
	for _, c := range cs {
		out.Commits = append(out.Commits, commitInfo(c))
	}
	out.CommitsTruncated = out.TotalCommits > int64(len(out.Commits))

	diff, err := gr.DiffWithOptions(out.MergeBase, out.HeadCommit, gitDiffOptions(o, out.Path))
	if err != nil {
		return Compare{}, err
	}
	out.Files, out.Patch, out.PatchTruncated = changes(diff, out.Path)
	return out, nil
}

// fullFileLines is how many lines of a file's diff are parsed when it is
// asked for alone; the patch is still cut at maxPatchBytes.
const fullFileLines = 100_000

// gitDiffOptions are the options for a diff of path alone, or of every
// file when path is empty. path is already clean.
func gitDiffOptions(o diffOpts, path string) git.DiffOptions {
	g := git.DiffOptions{IgnoreWhitespace: o.IgnoreWhitespace, Path: path}
	if path != "" {
		g.MaxFileLines = fullFileLines
	}
	return g
}

// changes is the files a diff touches and its patch, cut to maxPatchBytes.
// With only set, it is that file's alone.
func changes(diff *git.Diff, only string) ([]FileChange, string, bool) {
	if only != "" {
		diff.Files = slices.DeleteFunc(diff.Files, func(f *git.DiffFile) bool {
			return f.Name != only && f.OldName() != only
		})
	}
	files := make([]FileChange, 0, len(diff.Files))
	for _, f := range diff.Files {
		fc := FileChange{
			Path:      f.Name,
			Status:    "modified",
			Additions: f.NumAdditions(),
			Deletions: f.NumDeletions(),
			Binary:    f.IsBinary(),
			Truncated: f.IsIncomplete(),
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
		files = append(files, fc)
	}
	patch := diff.Patch()
	if len(patch) > maxPatchBytes {
		return files, truncateUTF8(patch, maxPatchBytes), true
	}
	return files, patch, false
}

// --- Search ---

// SearchMatch is one matching line.
type SearchMatch struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SearchFile is a file with matches.
type SearchFile struct {
	Path    string        `json:"path"`
	Matches []SearchMatch `json:"matches"`
	// More counts the file's matches past the ones shown.
	More int `json:"more,omitempty"`
}

// SearchResult is the files at a revision whose text contains a query.
type SearchResult struct {
	Ref       string       `json:"ref"`
	Commit    string       `json:"commit"`
	Query     string       `json:"query"`
	Files     []SearchFile `json:"files"`
	Truncated bool         `json:"truncated"`
}

// checkQuery refuses a query too short to be useful or too long to be sane.
func checkQuery(q string) (string, error) {
	q = strings.TrimSpace(q)
	if n := utf8.RuneCountInString(q); n < 2 || n > 200 || strings.ContainsAny(q, "\x00\n\r") {
		return "", badRequest("a search is 2 to 200 characters on one line")
	}
	return q, nil
}

// search finds text in the files at rev: case-insensitive, matched as
// written (not a regular expression), text files only.
func (b *browser) search(ctx context.Context, name, rev, q string) (SearchResult, error) {
	q, err := checkQuery(q)
	if err != nil {
		return SearchResult{}, err
	}
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return SearchResult{}, err
	}
	rev, c, err := resolve(gr, rev)
	if err != nil {
		return SearchResult{}, err
	}
	sha := c.ID.String()
	out := SearchResult{Ref: rev, Commit: sha, Query: q, Files: []SearchFile{}}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	total := 0
	err = scanGit(ctx, gr.Path, func(s string) error {
		f := strings.SplitN(s, "\x00", 3)
		if len(f) < 3 {
			return nil
		}
		p := strings.TrimPrefix(f[0], sha+":")
		n, _ := strconv.Atoi(f[1])
		last := len(out.Files) - 1
		if last < 0 || out.Files[last].Path != p {
			out.Files = append(out.Files, SearchFile{Path: p})
			last++
		}
		file := &out.Files[last]
		if len(file.Matches) >= maxFileMatches {
			file.More++
			return nil
		}
		if total == maxSearchMatches {
			out.Truncated = true
			return errStopScan
		}
		total++
		file.Matches = append(file.Matches, SearchMatch{Line: n, Text: truncateUTF8(f[2], maxMatchBytes)})
		return nil
	}, "-c", "core.quotePath=false", "grep", "-I", "-n", "-i", "-F", "-z", "-e", q, sha, "--")
	if exitCode(err) == 1 {
		return out, nil // no matches
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		out.Truncated = true
		return out, nil
	}
	return out, err
}

// filterRepos keeps the repositories whose name, project name or
// description contains q, ignoring case.
func filterRepos(rs []RepoInfo, q string) []RepoInfo {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return rs
	}
	out := rs[:0:0]
	for _, r := range rs {
		if strings.Contains(strings.ToLower(r.Name), q) ||
			strings.Contains(strings.ToLower(r.ProjectName), q) ||
			strings.Contains(strings.ToLower(r.Description), q) {
			out = append(out, r)
		}
	}
	return out
}

// --- Archives ---

// archiveFormats maps a format a caller asks for to its extension and type.
var archiveFormats = map[string][2]string{
	"":       {"tar.gz", "application/gzip"},
	"tar.gz": {"tar.gz", "application/gzip"},
	"tgz":    {"tar.gz", "application/gzip"},
	"zip":    {"zip", "application/zip"},
}

// archive streams a snapshot of the files at rev to w. before is called
// with the file name and type once the revision is known to exist; when it
// returns false no archive is made, as for a HEAD request.
func (b *browser) archive(ctx context.Context, name, rev, format string, w io.Writer, before func(filename, contentType string) bool) error {
	f, ok := archiveFormats[format]
	if !ok {
		return badRequest("unknown archive format %q: use tar.gz or zip", format)
	}
	r, gr, err := b.open(ctx, name)
	if err != nil {
		return err
	}
	rev, c, err := resolve(gr, rev)
	if err != nil {
		return err
	}
	prefix := safeFileName(gopath.Base(r.Name()) + "-" + rev)
	if !before(prefix+"."+f[0], f[1]) {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	args := []string{"archive", "--prefix=" + prefix + "/"}
	if f[0] == "zip" {
		return runGit(ctx, gr.Path, w, append(args, "--format=zip", c.ID.String())...)
	}
	// Compressed here, so the server needs no gzip program.
	gz := gzip.NewWriter(w)
	if err := runGit(ctx, gr.Path, gz, append(args, "--format=tar", c.ID.String())...); err != nil {
		return err
	}
	return gz.Close()
}

// safeFileName keeps letters, digits, dots, dashes and underscores.
func safeFileName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '-'
	}, s)
}

// --- Access ---

// Collaborator is a user given access to a repository.
type Collaborator struct {
	Username string `json:"username"`
	Access   string `json:"access"`
}

// collaborators lists a repository's collaborators, for its admins only.
func (b *browser) collaborators(ctx context.Context, name string) ([]Collaborator, error) {
	r, _, level, err := b.openLevel(ctx, name)
	if err != nil {
		return nil, err
	}
	// The same rule as `repo collab list` over SSH: anyone who may add
	// collaborators may see them.
	if level < access.ReadWriteAccess {
		return nil, fmt.Errorf("%w: only those with write access can see a repository's collaborators", errForbidden)
	}
	names, err := b.be.Collaborators(ctx, r.Name())
	if err != nil {
		return nil, err
	}
	out := make([]Collaborator, 0, len(names))
	for _, n := range names {
		lvl, ok, err := b.be.IsCollaborator(ctx, r.Name(), n)
		if err != nil || !ok {
			continue
		}
		out = append(out, Collaborator{Username: n, Access: lvl.String()})
	}
	slices.SortFunc(out, func(a, c Collaborator) int { return strings.Compare(a.Username, c.Username) })
	return out, nil
}
