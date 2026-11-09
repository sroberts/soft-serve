package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	gopath "path"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/utils"
	"github.com/dustin/go-humanize"
)

// The web pages. Every page is server-rendered HTML with no script, under a
// strict Content-Security-Policy; changes are plain form posts.
//
// Routes, with repository names that may contain slashes:
//
//	/                              repositories
//	/{repo}                        summary: files at the default branch, README
//	/{repo}/-/tree?ref=&path=      a directory
//	/{repo}/-/blob?ref=&path=      a file; &lines=10-25 selects lines
//	/{repo}/-/raw?ref=&path=       a file's bytes
//	/{repo}/-/find?ref=&q=         files by name
//	/{repo}/-/commits?ref=&page=   history; &q= and &author= search it
//	/{repo}/-/commit/{sha}         one commit and its diff (&view=split,
//	                               &w=1 without whitespace, &path= one file)
//	/{repo}/-/refs                 branches and tags
//	/{repo}/-/blame?ref=&path=     who last changed each line of a file
//	/{repo}/-/compare?base=&head=  what head has that base doesn't
//	/{repo}/-/search?ref=&q=       text in the files
//	/{repo}/-/archive?ref=&format= a snapshot as .tar.gz or .zip
//	/{repo}/-/commits.atom?ref=    an Atom feed of the history
//	/{repo}/-/access               your access, and collaborators (admins)
//	/{repo}/-/insights?ref=        languages, contributors and activity
//	/-/me                          your account
//	/-/new                         create or import a repository
//
// The pages that change things are in ui_manage.go, and your account's
// forms in ui_account.go.
//	/-/static/...                  stylesheets

//go:embed ui_templates/*.html ui_static/*
var uiFS embed.FS

var uiTemplates = template.Must(template.New("").Funcs(template.FuncMap{
	"ago":          ago,
	"bytes":        func(n int64) string { return humanize.Bytes(uint64(max(n, 0))) }, //nolint:gosec
	"short":        func(s string) string { return s[:min(len(s), 7)] },
	"repoURL":      repoURL,
	"pathParts":    pathParts,
	"parentDir":    parentDir,
	"commitsURL":   commitsURL,
	"authorURL":    authorURL,
	"lineURL":      lineURL,
	"linesURL":     linesURL,
	"distance":     distanceText,
	"agoPtr":       agoPtr,
	"static":       staticURL,
	"body":         messageBody,
	"dataPath":     dataPath,
	"archiveURL":   archiveURL,
	"compareURL":   compareURL,
	"accessText":   accessText,
	"mark":         markMatches,
	"isAdmin":      func(level string) bool { return level == access.AdminAccess.String() },
	"canWrite":     canWrite,
	"levels":       func() []string { return accessLevels },
	"deref":        func(b *bool) bool { return b != nil && *b },
	"hookForm":     hookFormArgs,
	"deleteRefURL": deleteRefURL,
	"keyShort":     keyShort,
}).ParseFS(uiFS, "ui_templates/*.html"))

// uiHandler serves the web pages.
type uiHandler struct {
	verifier *outpostVerifier
}

// page is what every template gets.
type page struct {
	Title    string
	SiteName string
	// SignedIn is the Outpost user, when the gateway fronts us; SoftServe
	// is the Soft Serve account their access comes from, if any.
	SignedIn  string
	SoftServe string
	Repo      *RepoInfo
	Ref       string
	Tab       string
	// RepoHome is set on a repository's front page, the one page that
	// shows its description, clone URLs and downloads. Deeper pages lead
	// with what they show instead.
	RepoHome bool
	// Feed is an Atom feed for the page, if it has one.
	Feed string
	// Notice says what the change that led here did.
	Notice string
	Data   any
}

func (h uiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	post := r.Method == http.MethodPost
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !post {
		// Pages are read, and forms are posted. Anything else is a path
		// no route has, as it was before the pages existed.
		renderNotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/-/static/") {
		serveStatic(w, r)
		return
	}

	c, err := identifyCaller(r, h.verifier)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="Soft Serve" charset="UTF-8", Token`)
		h.renderError(w, r, nil, http.StatusUnauthorized, "Those credentials aren't valid.")
		return
	}
	b := newBrowser(r.Context(), c.user, cloneBase(r, h.verifier))
	p := page{SiteName: config.FromContext(r.Context()).Name, SignedIn: c.outpostUser}
	if c.user != nil {
		p.SoftServe = c.user.Username()
	}
	if !post {
		p.Notice = notices[r.URL.Query().Get("done")]
	}

	rest := strings.Trim(r.URL.Path, "/")
	switch rest {
	case "":
		if !post {
			h.index(w, r, b, p)
			return
		}
	case "-/me":
		if !post {
			h.account(w, r, b, p)
			return
		}
	case "-/new":
		h.newRepo(w, r, b, p)
		return
	}
	if action, ok := strings.CutPrefix(rest, "-/me/"); ok {
		h.accountAction(w, r, b, p, action)
		return
	}
	name, action, _ := strings.Cut(rest, "/-/")
	if post && !isFormAction(action) {
		renderNotFound(w, r)
		return
	}
	// Redirect only a valid repository name: anything else (a leading "\\",
	// say, which browsers read as "//") could send the visitor off-site.
	if trimmed, ok := strings.CutSuffix(name, ".git"); ok && action == "" && utils.ValidateRepo(trimmed) == nil {
		http.Redirect(w, r, repoURL(trimmed, "", "", ""), http.StatusMovedPermanently)
		return
	}
	ctx := r.Context()
	info, err := b.repo(ctx, name)
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Repo = &info
	p.Title = info.Name
	q := r.URL.Query()
	p.Ref = q.Get("ref")

	switch {
	case action == "":
		h.summary(w, r, b, p)
	case action == "tree":
		h.tree(w, r, b, p, q.Get("path"))
	case action == "blob":
		h.blob(w, r, b, p, q.Get("path"))
	case action == "raw":
		serveRaw(w, r, b, info.Name)
	case action == "find":
		h.find(w, r, b, p, q.Get("q"))
	case action == "commits":
		pg, _ := strconv.Atoi(q.Get("page"))
		h.commits(w, r, b, p, commitFilter{Path: q.Get("path"), Query: q.Get("q"), Author: q.Get("author")}, pg)
	case strings.HasPrefix(action, "commit/"):
		h.commit(w, r, b, p, strings.TrimPrefix(action, "commit/"))
	case action == "insights":
		h.insights(w, r, b, p)
	case action == "refs":
		h.refs(w, r, b, p)
	case action == "blame":
		h.blame(w, r, b, p, q.Get("path"))
	case action == "compare":
		h.compare(w, r, b, p, q.Get("base"), q.Get("head"))
	case action == "search":
		h.search(w, r, b, p, q.Get("q"))
	case action == "archive":
		serveArchive(w, r, b, info.Name)
	case action == "commits.atom":
		h.feed(w, r, b, p, q.Get("path"))
	case action == "access":
		h.access(w, r, b, p)
	case action == "access/add":
		h.collab(w, r, b, p, "add")
	case action == "access/remove":
		h.collab(w, r, b, p, "remove")
	case action == "settings":
		h.settings(w, r, b, p)
	case action == "rename":
		h.rename(w, r, b, p)
	case action == "sync":
		h.syncMirror(w, r, b, p)
	case action == "delete":
		h.deleteRepo(w, r, b, p)
	case action == "refs/delete":
		h.deleteRef(w, r, b, p)
	case action == "webhooks":
		h.webhooks(w, r, b, p, "")
	case strings.HasPrefix(action, "webhooks/"):
		h.webhooks(w, r, b, p, strings.TrimPrefix(action, "webhooks/"))
	default:
		h.fail(w, r, p, errNotFound)
	}
}

type indexData struct {
	Repos []RepoInfo
	Query string
	Total int
}

func (h uiHandler) index(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	repos, err := b.repos(r.Context())
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	p.Title = p.SiteName
	p.Data = indexData{Repos: filterRepos(repos, q), Query: q, Total: len(repos)}
	h.render(w, r, "index", p)
}

type summaryData struct {
	Tree       *Tree
	Readme     template.HTML
	ReadmePath string
}

func (h uiHandler) summary(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "files"
	p.RepoHome = true
	d := summaryData{}
	if !p.Repo.Empty {
		t, err := b.tree(r.Context(), p.Repo.Name, p.Ref, "", true)
		if err != nil {
			h.fail(w, r, p, err)
			return
		}
		d.Tree = &t
		p.Ref = t.Ref
		d.Readme, d.ReadmePath = h.readme(r, b, p)
	}
	p.Data = d
	h.render(w, r, "summary", p)
}

// readme renders the repository's README for the summary page, or nothing.
func (h uiHandler) readme(r *http.Request, b *browser, p page) (template.HTML, string) {
	rd, err := b.readme(r.Context(), p.Repo.Name, p.Ref)
	if err != nil {
		return "", ""
	}
	if !isMarkdown(rd.Path) {
		return template.HTML("<pre class=\"plain\">" + template.HTMLEscapeString(rd.Content) + "</pre>"), rd.Path //nolint:gosec // escaped
	}
	target := func(fp string, image bool) string {
		if image {
			return repoURL(p.Repo.Name, "raw", p.Ref, fp)
		}
		return repoURL(p.Repo.Name, "blob", p.Ref, fp)
	}
	return renderMarkdown(rd.Content, gopath.Dir(rd.Path), target), rd.Path
}

func (h uiHandler) tree(w http.ResponseWriter, r *http.Request, b *browser, p page, path string) {
	p.Tab = "files"
	t, err := b.tree(r.Context(), p.Repo.Name, p.Ref, path, true)
	if errors.Is(err, errNotDir) {
		http.Redirect(w, r, repoURL(p.Repo.Name, "blob", p.Ref, path), http.StatusFound)
		return
	}
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Ref = t.Ref
	p.Title = t.Path + " · " + p.Repo.Name
	p.Data = summaryData{Tree: &t}
	h.render(w, r, "tree", p)
}

type blobData struct {
	Blob     Blob
	Code     template.HTML
	Lines    []codeLine
	Image    bool
	Markdown bool
	// Source shows a Markdown file's text rather than rendering it.
	Source    bool
	Sel       lineSel
	Permalink string
	// SourceURL and RenderedURL switch how a Markdown file is shown.
	SourceURL   string
	RenderedURL string
	ClearURL    string
}

func (h uiHandler) blob(w http.ResponseWriter, r *http.Request, b *browser, p page, path string) {
	p.Tab = "files"
	bl, err := b.blob(r.Context(), p.Repo.Name, p.Ref, path)
	if errors.Is(err, errIsDir) {
		http.Redirect(w, r, repoURL(p.Repo.Name, "tree", p.Ref, path), http.StatusFound)
		return
	}
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Ref = bl.Ref
	p.Title = bl.Path + " · " + p.Repo.Name
	q := r.URL.Query()
	d := blobData{Blob: bl, Sel: parseLines(q.Get("lines"))}
	ref := q.Get("ref") // as asked for: a permalink keeps its hash
	if ref != bl.Commit {
		d.Permalink = linesURL(p.Repo.Name, "blob", bl.Commit, bl.Path, d.Sel)
	}
	d.ClearURL = repoURL(p.Repo.Name, "blob", ref, bl.Path)
	if isMarkdown(bl.Path) && !bl.LFSMissing && !bl.Binary {
		d.Markdown = true
		d.Source = q.Get("plain") == "1" || d.Sel.Start > 0
		d.RenderedURL = repoURL(p.Repo.Name, "blob", ref, bl.Path)
		d.SourceURL = d.RenderedURL + "&plain=1"
	}
	switch {
	case bl.LFSMissing:
	case bl.Binary:
		d.Image = strings.HasPrefix(rawContentType(bl.Path, true), "image/")
	case d.Markdown && !d.Source:
		target := func(fp string, image bool) string {
			if image {
				return repoURL(p.Repo.Name, "raw", p.Ref, fp)
			}
			return repoURL(p.Repo.Name, "blob", p.Ref, fp)
		}
		d.Code = renderMarkdown(bl.Content, gopath.Dir(bl.Path), target)
	case bl.Content == "":
		// Left for the page to say the file is empty.
	default:
		d.Lines = codeLines(p.Repo.Name, ref, bl.Path, bl.Content, d.Sel)
	}
	p.Data = d
	h.render(w, r, "blob", p)
}

type commitsData struct {
	Page     CommitPage
	Prev     int
	Next     int
	Searched bool
	Err      string
}

func (h uiHandler) commits(w http.ResponseWriter, r *http.Request, b *browser, p page, f commitFilter, pg int) {
	p.Tab = "commits"
	cp, err := b.commits(r.Context(), p.Repo.Name, p.Ref, f, pg, defaultPerPage)
	if err != nil && statusFor(err) == http.StatusBadRequest && (f.Query != "" || f.Author != "") {
		// A search that can't run: say why beside the form.
		p.Title = "Commits · " + p.Repo.Name
		p.Data = commitsData{Page: CommitPage{Ref: p.Ref, Path: f.Path, Query: f.Query, Author: f.Author}, Err: publicMessage(err)}
		h.renderStatus(w, r, http.StatusBadRequest, "commits", p)
		return
	}
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Ref = cp.Ref
	p.Feed = feedURL(p.Repo.Name, p.Ref, cp.Path)
	p.Title = "Commits · " + p.Repo.Name
	if cp.Path != "" {
		p.Title = "History of " + cp.Path + " · " + p.Repo.Name
	}
	d := commitsData{Page: cp, Searched: cp.Query != "" || cp.Author != ""}
	if cp.Page > 1 {
		d.Prev = cp.Page - 1
	}
	if int64(cp.Page*cp.PerPage) < cp.Total {
		d.Next = cp.Page + 1
	}
	p.Data = d
	h.render(w, r, "commits", p)
}

func (h uiHandler) commit(w http.ResponseWriter, r *http.Request, b *browser, p page, sha string) {
	p.Tab = "commits"
	q := r.URL.Query()
	c, err := b.commit(r.Context(), p.Repo.Name, sha, diffOpts{IgnoreWhitespace: q.Get("w") == "1", Path: q.Get("path")})
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Title = c.Summary + " · " + p.Repo.Name
	p.Data = commitData{
		CommitDetail: c,
		Diff:         newDiffView(r.URL.Path, keep(q, "view", "w", "path"), c.Files, c.Patch, c.PatchTruncated),
	}
	h.render(w, r, "commit", p)
}

type commitData struct {
	CommitDetail
	Diff diffView
}

// keep is the parts of q named.
func keep(q url.Values, keys ...string) url.Values {
	out := url.Values{}
	for _, k := range keys {
		if v := q.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	return out
}

func (h uiHandler) refs(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "refs"
	refs, err := b.refs(r.Context(), p.Repo.Name)
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Title = "Branches and tags · " + p.Repo.Name
	p.Data = refs
	h.render(w, r, "refs", p)
}

func (h uiHandler) fail(w http.ResponseWriter, r *http.Request, p page, err error) {
	status := statusFor(err)
	msg := "Something went wrong on the server."
	switch {
	case errors.Is(err, errEmptyRepo):
		msg = "This repository has no commits yet."
	case status == http.StatusNotFound:
		msg = "There's nothing here, or you don't have access to it."
	case status == http.StatusBadRequest:
		msg = publicMessage(err)
	case status == http.StatusForbidden:
		msg = "You don't have enough access to this repository to see this."
	default:
		log.FromContext(r.Context()).Error("web", "path", r.URL.Path, "err", err)
	}
	h.renderError(w, r, &p, status, msg)
}

func (h uiHandler) renderError(w http.ResponseWriter, r *http.Request, p *page, status int, msg string) {
	if p == nil {
		p = &page{SiteName: config.FromContext(r.Context()).Name}
	}
	p.Title = fmt.Sprintf("%d %s", status, http.StatusText(status))
	p.Data = msg
	h.renderStatus(w, r, status, "error", *p)
}

func (h uiHandler) render(w http.ResponseWriter, r *http.Request, name string, p page) {
	h.write(w, r, name, p)
}

// renderStatus renders a page with a status other than 200.
func (h uiHandler) renderStatus(w http.ResponseWriter, r *http.Request, status int, name string, p page) {
	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, name, p); err != nil {
		log.FromContext(r.Context()).Error("web: render", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setPageHeaders(w)
	writeBody(w, r, status, buf.Bytes())
}

func (h uiHandler) write(w http.ResponseWriter, r *http.Request, name string, p page) {
	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, name, p); err != nil {
		log.FromContext(r.Context()).Error("web: render", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setPageHeaders(w)
	writeBody(w, r, http.StatusOK, buf.Bytes())
}

// setPageHeaders locks a page down: no script at all, styles and images
// only from here (and images over https, for README badges), no framing.
func setPageHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self' https: data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-cache")
}

// staticBody is a stylesheet's content, by name.
func staticBody(name string) []byte {
	switch name {
	case "style.css":
		b, _ := uiFS.ReadFile("ui_static/style.css")
		return b
	case "code.css":
		return highlightCSS()
	}
	return nil
}

var staticVersions sync.Map // name -> short content hash

// staticURL is a stylesheet's address, with a hash of its content, so a
// browser holding an older copy from its cache fetches the new one.
func staticURL(name string) string {
	v, ok := staticVersions.Load(name)
	if !ok {
		sum := sha256.Sum256(staticBody(name))
		v, _ = staticVersions.LoadOrStore(name, hex.EncodeToString(sum[:6]))
	}
	return "/-/static/" + name + "?v=" + v.(string)
}

func serveStatic(w http.ResponseWriter, r *http.Request) {
	body := staticBody(strings.TrimPrefix(r.URL.Path, "/-/static/"))
	if body == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if name := strings.TrimPrefix(r.URL.Path, "/-/static/"); r.URL.RawQuery != "" && staticURL(name) == r.URL.Path+"?"+r.URL.RawQuery {
		// Pages ask for it by a hash of its content, so it never changes.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeBody(w, r, http.StatusOK, body)
}

// minGzip is the smallest body worth compressing.
const minGzip = 1 << 10

// writeBody writes a page or stylesheet, gzipped when the client takes it.
// Pages of long diffs or files run to a megabyte of repetitive markup, which
// gzip shrinks about tenfold: it matters on a phone.
func writeBody(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	hdr := w.Header()
	hdr.Add("Vary", "Accept-Encoding")
	if len(body) < minGzip || !acceptsGzip(r) {
		w.WriteHeader(status)
		w.Write(body) //nolint:errcheck
		return
	}
	hdr.Set("Content-Encoding", "gzip")
	hdr.Del("Content-Length")
	w.WriteHeader(status)
	zw, _ := gzip.NewWriterLevel(w, gzip.DefaultCompression)
	zw.Write(body) //nolint:errcheck
	zw.Close()     //nolint:errcheck
}

// acceptsGzip says whether a request's Accept-Encoding allows gzip.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

// repoURL is a page of a repository. Paths are relative to our own host,
// so no hostname is ever written into a page.
func repoURL(name, action, ref, p string) string {
	u := "/" + name
	if action != "" {
		u += "/-/" + action
	}
	q := url.Values{}
	if ref != "" {
		q.Set("ref", ref)
	}
	if p != "" {
		q.Set("path", p)
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// messageBody is a commit message without its summary line.
func messageBody(msg string) string {
	_, rest, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	return strings.TrimSpace(rest)
}

// parentDir is the directory above p, "" for the root.
func parentDir(p string) string {
	d := gopath.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// dataPath is the repository path a page shows, for its breadcrumbs.
func dataPath(d any) string {
	switch v := d.(type) {
	case summaryData:
		if v.Tree != nil {
			return v.Tree.Path
		}
	case blobData:
		return v.Blob.Path
	case blameData:
		return v.Blame.Path
	}
	return ""
}

type pathPart struct {
	Name string
	Path string
	// Last marks the page's own file or folder, which isn't a link.
	Last bool
}

// pathParts splits a path into breadcrumbs.
func pathParts(p string) []pathPart {
	if p == "" {
		return nil
	}
	var parts []pathPart
	acc := ""
	for _, seg := range strings.Split(p, "/") {
		acc = gopath.Join(acc, seg)
		parts = append(parts, pathPart{Name: seg, Path: acc})
	}
	parts[len(parts)-1].Last = true
	return parts
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return humanize.Time(t)
}
