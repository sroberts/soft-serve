package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	gopath "path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/soft-serve/git"
	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/ops"
)

const (
	// aheadBehindBranches and aheadBehindBudget bound counting how far each
	// branch is from the default one. Branches past either go uncounted.
	aheadBehindBranches = 100
	aheadBehindBudget   = 3 * time.Second
	// maxFindResults caps the files a find returns.
	maxFindResults = 100
	// statsBudget bounds each walk the stats make of a repository.
	statsBudget = 10 * time.Second
	// maxContributors caps the contributors the stats list.
	maxContributors = 25
	// statsWeeks is how many weeks of activity the stats count.
	statsWeeks = 52
)

// --- Searching the history ---

// checkAuthor refuses an author filter that isn't one sane line.
func checkAuthor(a string) (string, error) {
	a = strings.TrimSpace(a)
	if n := utf8.RuneCountInString(a); n > 100 || strings.ContainsAny(a, "\x00\n\r") {
		return "", badRequest("an author is up to 100 characters on one line")
	}
	return a, nil
}

// searchCommits is a page of the commits at sha whose message contains
// f.Query and whose author contains f.Author, ignoring case and matched as
// written. Both are handed to git as fixed strings, never patterns, and
// the path as a literal path, never pathspec magic such as :(exclude). page
// and perPage are already in range.
func (b *browser) searchCommits(ctx context.Context, gr *git.Repository, rev, sha string, f commitFilter, page, perPage int) (CommitPage, error) {
	var err error
	if f.Query != "" {
		if f.Query, err = checkQuery(f.Query); err != nil {
			return CommitPage{}, err
		}
	}
	if f.Author, err = checkAuthor(f.Author); err != nil {
		return CommitPage{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	filters := []string{"--regexp-ignore-case", "--fixed-strings"}
	if f.Query != "" {
		filters = append(filters, "--grep="+f.Query)
	}
	if f.Author != "" {
		filters = append(filters, "--author="+f.Author)
	}
	tail := []string{sha, "--"}
	if f.Path != "" {
		tail = append(tail, f.Path)
	}

	count, err := gitOutput(ctx, gr.Path, slices.Concat([]string{"--literal-pathspecs", "rev-list", "--count"}, filters, tail)...)
	if err != nil {
		return CommitPage{}, err
	}
	total, _ := strconv.ParseInt(strings.TrimSpace(string(count)), 10, 64)
	out := CommitPage{Ref: rev, Path: f.Path, Query: f.Query, Author: f.Author, Page: page, PerPage: perPage, Total: total, Commits: []CommitInfo{}}

	list, err := gitOutput(ctx, gr.Path, slices.Concat([]string{
		"--literal-pathspecs", "log", "--format=%H",
		"--skip=" + strconv.Itoa((page-1)*perPage), "--max-count=" + strconv.Itoa(perPage),
	}, filters, tail)...)
	if err != nil {
		return CommitPage{}, err
	}
	for _, h := range strings.Fields(string(list)) {
		c, err := gr.CatFileCommit(h)
		if err != nil {
			return CommitPage{}, err
		}
		out.Commits = append(out.Commits, commitInfo(c))
	}
	return out, nil
}

// --- How far each branch is from the default one ---

// aheadBehind counts, for each branch but the default, the commits it has
// that the default branch doesn't, and the other way round.
func aheadBehind(ctx context.Context, gr *git.Repository, refs *Refs) {
	var base string
	for _, br := range refs.Branches {
		if br.Name == refs.DefaultBranch {
			base = br.Commit
		}
	}
	if base == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, aheadBehindBudget)
	defer cancel()
	counted := 0
	for i := range refs.Branches {
		br := &refs.Branches[i]
		if br.Name == refs.DefaultBranch {
			continue
		}
		if counted++; counted > aheadBehindBranches || ctx.Err() != nil {
			return
		}
		out, err := gitOutput(ctx, gr.Path, "rev-list", "--left-right", "--count", base+"..."+br.Commit, "--")
		if err != nil {
			continue
		}
		f := strings.Fields(string(out))
		if len(f) != 2 {
			continue
		}
		behind, err1 := strconv.ParseInt(f[0], 10, 64)
		ahead, err2 := strconv.ParseInt(f[1], 10, 64)
		if err1 == nil && err2 == nil {
			br.Ahead, br.Behind = &ahead, &behind
		}
	}
}

// --- Mirrors ---

// mirrorDetails is where a mirror is copied from, with any credentials
// taken out, and when it last fetched.
func mirrorDetails(ctx context.Context, gr *git.Repository) (string, *time.Time) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var from string
	if out, err := gitOutput(ctx, gr.Path, "config", "--get", "remote.origin.url"); err == nil {
		from = redactRemote(strings.TrimSpace(string(out)))
	}
	var synced *time.Time
	if fi, err := os.Stat(filepath.Join(gr.Path, "FETCH_HEAD")); err == nil {
		t := fi.ModTime().UTC()
		synced = &t
	}
	return from, synced
}

// redactRemote is a remote's URL fit to show: no user, password, query or
// fragment, and nothing at all for a path on the server.
func redactRemote(raw string) string {
	if raw == "" || strings.ContainsAny(raw, "\x00\n\r") || strings.HasPrefix(strings.ToLower(raw), "file:") {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		if u.Scheme == "file" {
			return ""
		}
		u.User, u.RawQuery, u.Fragment, u.ForceQuery = nil, "", "", false
		return u.String()
	}
	// A URL that didn't parse (a password holding '#', '/' or '?', say) is
	// not scp-like, and showing it as-is could show its credentials.
	if strings.Contains(raw, "://") {
		return ""
	}
	// scp-like: [user@]host:path. A Windows drive (C:\...) or a local path
	// has no host to show.
	host, p, ok := strings.Cut(raw, ":")
	if !ok || strings.ContainsAny(host, "/\\") || len(host) < 2 || strings.HasPrefix(p, "\\") {
		return ""
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if host == "" {
		return ""
	}
	return host + ":" + p
}

// --- Finding files by name ---

// FileList is the files at a revision whose path matches a query.
type FileList struct {
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
	Query  string `json:"query"`
	// Total counts every match; Files are the best of them.
	Total     int      `json:"total"`
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated"`
}

// findFiles lists the files at rev whose path holds q's characters in
// order, ignoring case: "brgo" finds browse.go. The closest matches come
// first: q in the file's name, then in its path, then spread out.
func (b *browser) findFiles(ctx context.Context, name, rev, q string) (FileList, error) {
	q = strings.TrimSpace(q)
	if n := utf8.RuneCountInString(q); n < 1 || n > 200 || strings.ContainsAny(q, "\x00\n\r") {
		return FileList{}, badRequest("a file search is 1 to 200 characters on one line")
	}
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return FileList{}, err
	}
	rev, c, err := resolve(gr, rev)
	if err != nil {
		return FileList{}, err
	}
	out := FileList{Ref: rev, Commit: c.ID.String(), Query: q, Files: []string{}}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	data, err := gitOutput(ctx, gr.Path, "ls-tree", "-r", "-z", "--name-only", out.Commit)
	if err != nil {
		return FileList{}, err
	}
	type hit struct {
		path  string
		score int
	}
	var hits []hit
	lq := strings.ToLower(q)
	for p := range bytes.SplitSeq(data, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		path := string(p)
		if s, ok := findScore(strings.ToLower(path), lq); ok {
			hits = append(hits, hit{path, s})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		if a.score != b.score {
			return a.score - b.score
		}
		if len(a.path) != len(b.path) {
			return len(a.path) - len(b.path)
		}
		return strings.Compare(a.path, b.path)
	})
	out.Total = len(hits)
	for _, h := range hits[:min(len(hits), maxFindResults)] {
		out.Files = append(out.Files, h.path)
	}
	out.Truncated = out.Total > len(out.Files)
	return out, nil
}

// findScore ranks how well path matches q, both lowercased: lower is better.
func findScore(path, q string) (int, bool) {
	base := gopath.Base(path)
	switch {
	case base == q:
		return 0, true
	case strings.HasPrefix(base, q):
		return 1, true
	case strings.Contains(base, q):
		return 2, true
	case strings.Contains(path, q):
		return 3, true
	}
	// q's characters in order, scored by how spread out they are, worse
	// for each directory the match crosses and for starting outside the
	// file's name. Every place q could start is tried.
	first, _ := utf8.DecodeRuneInString(q)
	baseAt := len(path) - len(base)
	best, found := 0, false
	for start := 0; start < len(path); {
		i := strings.IndexRune(path[start:], first)
		if i < 0 {
			break
		}
		start += i
		gaps, end, ok := spreadFrom(path, q, start)
		if !ok {
			break // no later start can fit q either
		}
		score := gaps + 2*strings.Count(path[start:end], "/")
		switch {
		case start < baseAt:
			score += 8
		case start == baseAt:
			score -= 3 // q starts where the name does
		}
		if !found || score < best {
			best, found = score, true
		}
		start += utf8.RuneLen(first)
	}
	if !found {
		return 0, false
	}
	return 7 + best, true // always after the tiers above
}

// spreadFrom matches q's characters in order in path from start, which
// holds q's first, and counts the characters skipped between them.
func spreadFrom(path, q string, start int) (gaps, end int, ok bool) {
	pos, prev := start, -1
	for _, r := range q {
		i := strings.IndexRune(path[pos:], r)
		if i < 0 {
			return 0, 0, false
		}
		at := pos + i
		if prev >= 0 {
			gaps += at - prev
		}
		prev = at + utf8.RuneLen(r)
		pos = prev
	}
	return gaps, prev, true
}

// findMarks is where each of q's characters falls in path, for marking a
// match: the run of q itself when path contains it, else each character.
func findMarks(path, q string) [][2]int {
	lp, lq := strings.ToLower(path), strings.ToLower(q)
	if len(lp) != len(path) || lq == "" {
		return nil
	}
	if i := strings.LastIndex(lp, lq); i >= 0 && i >= strings.LastIndex(lp, "/") {
		return [][2]int{{i, i + len(lq)}}
	}
	if i := strings.Index(lp, lq); i >= 0 {
		return [][2]int{{i, i + len(lq)}}
	}
	// In order, in the file's name if they all fit there.
	if m := subsequenceRuns(lp, lq, strings.LastIndex(lp, "/")+1); m != nil {
		return m
	}
	return subsequenceRuns(lp, lq, 0)
}

// subsequenceRuns is where q's characters fall in s, in order from from,
// joined into runs; nil if they don't all.
func subsequenceRuns(s, q string, from int) [][2]int {
	var marks [][2]int
	at := from
	for _, r := range q {
		i := strings.IndexRune(s[at:], r)
		if i < 0 {
			return nil
		}
		start := at + i
		end := start + utf8.RuneLen(r)
		if n := len(marks); n > 0 && marks[n-1][1] == start {
			marks[n-1][1] = end
		} else {
			marks = append(marks, [2]int{start, end})
		}
		at = end
	}
	return marks
}

// --- Stats ---

// LanguageShare is how much of a repository is in one language.
type LanguageShare struct {
	Name    string  `json:"name"`
	Bytes   int64   `json:"bytes"`
	Files   int     `json:"files"`
	Percent float64 `json:"percent"`
}

// Contributor is someone who authored commits, by the name they used.
type Contributor struct {
	Name    string `json:"name"`
	Commits int    `json:"commits"`
}

// WeekCount is the commits made in the week starting on Monday Week.
type WeekCount struct {
	Week    time.Time `json:"week"`
	Commits int       `json:"commits"`
}

// Stats describe a repository at a revision.
type Stats struct {
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
	// Files and Bytes count everything in the tree; Languages only the
	// code in it, leaving out vendored files and prose and data formats.
	Files     int             `json:"files"`
	Bytes     int64           `json:"bytes"`
	Languages []LanguageShare `json:"languages"`
	// TotalCommits counts the history; Contributors are its most frequent
	// authors, leaving out merges, of TotalContributors.
	TotalCommits      int64         `json:"total_commits"`
	Contributors      []Contributor `json:"contributors"`
	TotalContributors int           `json:"total_contributors"`
	// Weekly is the commits in each of the last 52 weeks, oldest first.
	Weekly []WeekCount `json:"weekly"`
	// Partial is set when a time limit cut a count short.
	Partial bool `json:"partial"`
}

// notCode are languages that aren't counted as code: prose and data.
var notCode = map[string]bool{
	"markdown": true, "restructuredtext": true, "plaintext": true, "json": true,
	"yaml": true, "toml": true, "ini": true, "diff": true, "csv": true, "org mode": true,
	"text only": true,
}

// notCodeFiles are files whose names a lexer mistakes for code: go.mod is
// not AMPL.
var notCodeFiles = map[string]bool{"go.mod": true, "go.sum": true, "go.work": true, "go.work.sum": true}

// vendoredDirs are directories of code that isn't the repository's own.
var vendoredDirs = []string{"vendor/", "node_modules/", "third_party/", "third-party/", "bower_components/"}

func isVendored(p string) bool {
	for _, d := range vendoredDirs {
		if strings.HasPrefix(p, d) || strings.Contains(p, "/"+d) {
			return true
		}
	}
	return false
}

// languageOf names the language of a file by its name, or "".
func languageOf(p string, cache map[string]string) string {
	base := gopath.Base(p)
	if notCodeFiles[base] {
		return ""
	}
	key := strings.ToLower(gopath.Ext(base))
	if key == "" {
		key = base
	}
	if l, ok := cache[key]; ok {
		return l
	}
	l := ""
	if lx := lexers.Match(base); lx != nil && !notCode[strings.ToLower(lx.Config().Name)] {
		l = lx.Config().Name
	}
	cache[key] = l
	return l
}

func (b *browser) stats(ctx context.Context, name, rev string) (Stats, error) {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return Stats{}, err
	}
	rev, c, err := resolve(gr, rev)
	if err != nil {
		return Stats{}, err
	}
	sha := c.ID.String()
	out := Stats{Ref: rev, Commit: sha, Languages: []LanguageShare{}, Contributors: []Contributor{}}

	// Languages, from the size of every file in the tree.
	lctx, cancel := context.WithTimeout(ctx, statsBudget)
	defer cancel()
	byLang := map[string]*LanguageShare{}
	cache := map[string]string{}
	var code int64
	err = scanGit(lctx, gr.Path, func(line string) error {
		meta, p, ok := strings.Cut(line, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 || f[1] != "blob" {
			return nil
		}
		size, _ := strconv.ParseInt(f[3], 10, 64)
		out.Files++
		out.Bytes += size
		if isVendored(p) {
			return nil
		}
		l := languageOf(p, cache)
		if l == "" {
			return nil
		}
		s := byLang[l]
		if s == nil {
			s = &LanguageShare{Name: l}
			byLang[l] = s
		}
		s.Bytes += size
		s.Files++
		code += size
		return nil
	}, "-c", "core.quotePath=false", "ls-tree", "-r", "-l", sha)
	if errors.Is(lctx.Err(), context.DeadlineExceeded) {
		out.Partial = true
	} else if err != nil {
		return Stats{}, err
	}
	for _, s := range byLang {
		if code > 0 {
			s.Percent = float64(s.Bytes) * 100 / float64(code)
		}
		out.Languages = append(out.Languages, *s)
	}
	slices.SortFunc(out.Languages, func(a, b LanguageShare) int {
		if a.Bytes != b.Bytes {
			return int(b.Bytes - a.Bytes)
		}
		return strings.Compare(a.Name, b.Name)
	})

	if out.TotalCommits, err = gr.RevListCount([]string{sha}); err != nil {
		return Stats{}, err
	}

	// Contributors, by commits authored.
	cctx, cancel2 := context.WithTimeout(ctx, statsBudget)
	defer cancel2()
	err = scanGit(cctx, gr.Path, func(line string) error {
		n, who, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			return nil
		}
		out.TotalContributors++
		if len(out.Contributors) < maxContributors {
			commits, _ := strconv.Atoi(n)
			out.Contributors = append(out.Contributors, Contributor{Name: who, Commits: commits})
		}
		return nil
	}, "shortlog", "--summary", "--numbered", "--no-merges", sha, "--")
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		out.Partial = true
	} else if err != nil {
		return Stats{}, err
	}

	// Commits per week, for a year up to this week.
	now := time.Now().UTC()
	thisWeek := weekStart(now)
	first := thisWeek.AddDate(0, 0, -7*(statsWeeks-1))
	out.Weekly = make([]WeekCount, statsWeeks)
	for i := range out.Weekly {
		out.Weekly[i].Week = first.AddDate(0, 0, 7*i)
	}
	wctx, cancel3 := context.WithTimeout(ctx, statsBudget)
	defer cancel3()
	err = scanGit(wctx, gr.Path, func(line string) error {
		secs, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if err != nil {
			return nil
		}
		i := int(weekStart(time.Unix(secs, 0).UTC()).Sub(first).Hours() / (24 * 7))
		if i >= 0 && i < statsWeeks {
			out.Weekly[i].Commits++
		}
		return nil
	}, "log", "--format=%ct", "--since="+first.Format(time.RFC3339), sha, "--")
	if errors.Is(wctx.Err(), context.DeadlineExceeded) {
		out.Partial = true
	} else if err != nil {
		return Stats{}, err
	}
	return out, nil
}

// weekStart is the Monday at midnight UTC starting t's week.
func weekStart(t time.Time) time.Time {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// --- The caller's account ---

// AccountKey is one of a user's SSH keys.
type AccountKey = ops.PublicKey

// AccountToken is one of a user's access tokens. The token itself is never
// shown again after it is made.
type AccountToken struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Expired   bool       `json:"expired"`
}

// AccountRepo is a repository the user has more than read access to.
type AccountRepo struct {
	Name    string `json:"name"`
	Access  string `json:"access"`
	Private bool   `json:"private"`
	Hidden  bool   `json:"hidden"`
}

// Account is who the caller is here, and what they have.
type Account struct {
	SignedIn bool   `json:"signed_in"`
	Username string `json:"username,omitempty"`
	Admin    bool   `json:"admin"`
	// OutpostUser is who the login gateway says the caller is, when it
	// fronts this server.
	OutpostUser string         `json:"outpost_user,omitempty"`
	Keys        []AccountKey   `json:"keys"`
	Tokens      []AccountToken `json:"tokens"`
	// Repos are the repositories the user can push to or administer.
	Repos []AccountRepo `json:"repos"`
}

// account describes the caller's Soft Serve account, or, for an anonymous
// caller, that there isn't one.
func (b *browser) account(ctx context.Context, outpostUser string) (Account, error) {
	out := Account{OutpostUser: outpostUser, Keys: []AccountKey{}, Tokens: []AccountToken{}, Repos: []AccountRepo{}}
	if b.user == nil {
		return out, nil
	}
	out.SignedIn = true
	out.Username = b.user.Username()
	out.Admin = b.user.IsAdmin()
	for _, k := range b.user.PublicKeys() {
		out.Keys = append(out.Keys, ops.PublicKeyOf(k))
	}
	toks, err := b.be.ListAccessTokens(ctx, b.user)
	if err != nil {
		return Account{}, fmt.Errorf("access tokens: %w", err)
	}
	now := time.Now()
	for _, t := range toks {
		at := AccountToken{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt.UTC()}
		if !t.ExpiresAt.IsZero() {
			e := t.ExpiresAt.UTC()
			at.ExpiresAt = &e
			at.Expired = e.Before(now)
		}
		out.Tokens = append(out.Tokens, at)
	}
	repos, err := b.be.Repositories(ctx)
	if err != nil {
		return Account{}, err
	}
	for _, r := range repos {
		level := b.be.AccessLevelForUser(ctx, r.Name(), b.user)
		if level < access.ReadWriteAccess {
			continue
		}
		out.Repos = append(out.Repos, AccountRepo{Name: r.Name(), Access: level.String(), Private: r.IsPrivate(), Hidden: r.IsHidden()})
	}
	slices.SortFunc(out.Repos, func(a, b AccountRepo) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}
