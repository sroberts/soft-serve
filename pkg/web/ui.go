package web

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	gopath "path"
	"strconv"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/dustin/go-humanize"
)

// The read-only web pages. Every page is server-rendered HTML with no
// script, under a strict Content-Security-Policy.
//
// Routes, with repository names that may contain slashes:
//
//	/                              repositories
//	/{repo}                        summary: files at the default branch, README
//	/{repo}/-/tree?ref=&path=      a directory
//	/{repo}/-/blob?ref=&path=      a file
//	/{repo}/-/raw?ref=&path=       a file's bytes
//	/{repo}/-/commits?ref=&page=   history
//	/{repo}/-/commit/{sha}         one commit and its diff
//	/{repo}/-/refs                 branches and tags
//	/-/static/...                  stylesheets

//go:embed ui_templates/*.html ui_static/*
var uiFS embed.FS

var uiTemplates = template.Must(template.New("").Funcs(template.FuncMap{
	"ago":       ago,
	"bytes":     func(n int64) string { return humanize.Bytes(uint64(max(n, 0))) }, //nolint:gosec
	"short":     func(s string) string { return s[:min(len(s), 7)] },
	"repoURL":   repoURL,
	"pathParts": pathParts,
	"diffClass": diffClass,
	"lines":     func(s string) []string { return strings.Split(strings.TrimSuffix(s, "\n"), "\n") },
	"parentDir": parentDir,
	"pageURL":   pageURL,
	"body":      messageBody,
	"dataPath":  dataPath,
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
	Data      any
}

func (h uiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Pages are only read. Anything else is a path no route has, as
		// it was before the pages existed.
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

	rest := strings.Trim(r.URL.Path, "/")
	if rest == "" {
		h.index(w, r, b, p)
		return
	}
	name, action, _ := strings.Cut(rest, "/-/")
	if trimmed, ok := strings.CutSuffix(name, ".git"); ok && action == "" {
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
	case action == "commits":
		pg, _ := strconv.Atoi(q.Get("page"))
		h.commits(w, r, b, p, q.Get("path"), pg)
	case strings.HasPrefix(action, "commit/"):
		h.commit(w, r, b, p, strings.TrimPrefix(action, "commit/"))
	case action == "refs":
		h.refs(w, r, b, p)
	default:
		h.fail(w, r, p, errNotFound)
	}
}

func (h uiHandler) index(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	repos, err := b.repos(r.Context())
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Title = p.SiteName
	p.Data = repos
	h.render(w, r, "index", p)
}

type summaryData struct {
	Tree       *Tree
	Readme     template.HTML
	ReadmePath string
}

func (h uiHandler) summary(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "files"
	d := summaryData{}
	if !p.Repo.Empty {
		t, err := b.tree(r.Context(), p.Repo.Name, p.Ref, "")
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
	t, err := b.tree(r.Context(), p.Repo.Name, p.Ref, path)
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
	Image    bool
	Markdown bool
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
	d := blobData{Blob: bl}
	switch {
	case bl.LFSMissing:
	case bl.Binary:
		d.Image = strings.HasPrefix(rawContentType(bl.Path, true), "image/")
	case isMarkdown(bl.Path):
		target := func(fp string, image bool) string {
			if image {
				return repoURL(p.Repo.Name, "raw", p.Ref, fp)
			}
			return repoURL(p.Repo.Name, "blob", p.Ref, fp)
		}
		d.Code = renderMarkdown(bl.Content, gopath.Dir(bl.Path), target)
		d.Markdown = true
	default:
		d.Code = highlight(gopath.Base(bl.Path), bl.Content)
	}
	p.Data = d
	h.render(w, r, "blob", p)
}

type commitsData struct {
	Page CommitPage
	Prev int
	Next int
}

func (h uiHandler) commits(w http.ResponseWriter, r *http.Request, b *browser, p page, path string, pg int) {
	p.Tab = "commits"
	cp, err := b.commits(r.Context(), p.Repo.Name, p.Ref, path, pg, defaultPerPage)
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Ref = cp.Ref
	p.Title = "Commits · " + p.Repo.Name
	if cp.Path != "" {
		p.Title = "History of " + cp.Path + " · " + p.Repo.Name
	}
	d := commitsData{Page: cp}
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
	c, err := b.commit(r.Context(), p.Repo.Name, sha)
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Title = c.Summary + " · " + p.Repo.Name
	p.Data = c
	h.render(w, r, "commit", p)
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
	w.WriteHeader(status)
	h.write(w, r, "error", *p)
}

func (h uiHandler) render(w http.ResponseWriter, r *http.Request, name string, p page) {
	h.write(w, r, name, p)
}

func (h uiHandler) write(w http.ResponseWriter, r *http.Request, name string, p page) {
	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, name, p); err != nil {
		log.FromContext(r.Context()).Error("web: render", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setPageHeaders(w)
	w.Write(buf.Bytes()) //nolint:errcheck
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

func serveStatic(w http.ResponseWriter, r *http.Request) {
	var body []byte
	switch strings.TrimPrefix(r.URL.Path, "/-/static/") {
	case "style.css":
		b, err := uiFS.ReadFile("ui_static/style.css")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body = b
	case "code.css":
		body = highlightCSS()
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(body) //nolint:errcheck
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

// pageURL is a page of a repository's history, or of one path's.
func pageURL(name, ref, p string, n int) string {
	q := url.Values{"page": {strconv.Itoa(n)}}
	if ref != "" {
		q.Set("ref", ref)
	}
	if p != "" {
		q.Set("path", p)
	}
	return "/" + name + "/-/commits?" + q.Encode()
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
	}
	return ""
}

type pathPart struct {
	Name string
	Path string
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
	return parts
}

// diffClass colours a line of a unified diff.
func diffClass(line string) string {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "):
		return "meta"
	case strings.HasPrefix(line, "@@"):
		return "hunk"
	case strings.HasPrefix(line, "+"):
		return "add"
	case strings.HasPrefix(line, "-"):
		return "del"
	}
	return ""
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return humanize.Time(t)
}
