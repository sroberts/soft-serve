package web

import (
	"bytes"
	"encoding/xml"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	gopath "path"
	"slices"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/pkg/access"
)

// Pages for blame, comparing, searching and access, and the Atom feed.

// blameRow is one line of a blame page. The first line of each hunk carries
// its commit and how many lines the hunk spans.
type blameRow struct {
	Line   int
	Code   template.HTML
	Commit *CommitBrief
	Span   int
}

type blameData struct {
	Blame     Blame
	Rows      []blameRow
	Permalink string
}

func (h uiHandler) blame(w http.ResponseWriter, r *http.Request, b *browser, p page, path string) {
	p.Tab = "files"
	bl, err := b.blame(r.Context(), p.Repo.Name, p.Ref, path)
	if errors.Is(err, errIsDir) {
		http.Redirect(w, r, repoURL(p.Repo.Name, "tree", p.Ref, path), http.StatusFound)
		return
	}
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Ref = bl.Ref
	p.Title = "Blame " + bl.Path + " · " + p.Repo.Name

	slices.SortFunc(bl.Hunks, func(a, c BlameHunk) int { return a.Start - c.Start })
	var src []string
	for _, hk := range bl.Hunks {
		src = append(src, hk.Lines...)
	}
	code := highlightLines(gopath.Base(bl.Path), strings.Join(src, "\n"))
	d := blameData{Blame: bl}
	if r.URL.Query().Get("ref") != bl.Commit {
		d.Permalink = repoURL(p.Repo.Name, "blame", bl.Commit, bl.Path)
	}
	i := 0
	for _, hk := range bl.Hunks {
		c := bl.Commits[hk.Commit]
		for j := range hk.Lines {
			row := blameRow{Line: hk.Start + j}
			if i < len(code) {
				row.Code = code[i]
			}
			if j == 0 {
				row.Commit = &c
				row.Span = len(hk.Lines)
			}
			d.Rows = append(d.Rows, row)
			i++
		}
	}
	p.Data = d
	h.render(w, r, "blame", p)
}

type compareData struct {
	Base, Head string
	Refs       Refs
	Result     *Compare
	Diff       diffView
	Err        string
}

func (h uiHandler) compare(w http.ResponseWriter, r *http.Request, b *browser, p page, base, head string) {
	p.Tab = "compare"
	p.Title = "Compare · " + p.Repo.Name
	refs, err := b.refs(r.Context(), p.Repo.Name)
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	if base == "" {
		base = refs.DefaultBranch
	}
	d := compareData{Base: base, Head: head, Refs: refs}
	if head == "" || p.Repo.Empty {
		p.Data = d
		h.render(w, r, "compare", p)
		return
	}
	q := r.URL.Query()
	c, err := b.compare(r.Context(), p.Repo.Name, base, head, diffOpts{IgnoreWhitespace: q.Get("w") == "1", Path: q.Get("path")})
	switch {
	case err == nil:
		d.Result = &c
		d.Diff = newDiffView(r.URL.Path, keep(q, "base", "head", "view", "w", "path"), c.Files, c.Patch, c.PatchTruncated)
		p.Title = c.Base + "..." + c.Head + " · " + p.Repo.Name
	case statusFor(err) == http.StatusInternalServerError:
		h.fail(w, r, p, err)
		return
	default:
		// A ref that doesn't exist or doesn't make sense: say so beside
		// the form, so it can be fixed.
		d.Err = publicMessage(err)
		p.Data = d
		h.renderStatus(w, r, statusFor(err), "compare", p)
		return
	}
	p.Data = d
	h.render(w, r, "compare", p)
}

type searchData struct {
	Query  string
	Result *SearchResult
	Err    string
}

func (h uiHandler) search(w http.ResponseWriter, r *http.Request, b *browser, p page, q string) {
	p.Tab = "search"
	p.Title = "Search · " + p.Repo.Name
	d := searchData{Query: strings.TrimSpace(q)}
	if d.Query == "" || p.Repo.Empty {
		p.Data = d
		h.render(w, r, "search", p)
		return
	}
	res, err := b.search(r.Context(), p.Repo.Name, p.Ref, d.Query)
	switch {
	case err == nil:
		d.Result = &res
		p.Ref = res.Ref
		p.Title = d.Query + " · Search · " + p.Repo.Name
	case statusFor(err) == http.StatusInternalServerError:
		h.fail(w, r, p, err)
		return
	default:
		d.Err = publicMessage(err)
		p.Data = d
		h.renderStatus(w, r, statusFor(err), "search", p)
		return
	}
	p.Data = d
	h.render(w, r, "search", p)
}

type accessData struct {
	Collaborators []Collaborator
	// The add form as last submitted, and why it failed.
	Username, Level string
	Err             string
}

func (h uiHandler) access(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "access"
	p.Title = "Access · " + p.Repo.Name
	cs, err := b.collaborators(r.Context(), p.Repo.Name)
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Data = accessData{Collaborators: cs}
	h.render(w, r, "access", p)
}

// markMatches escapes a line of a search result and marks where q occurs in
// it, ignoring case as the search did.
func markMatches(text, q string) template.HTML {
	if q == "" {
		return template.HTML(template.HTMLEscapeString(text)) //nolint:gosec // escaped
	}
	var b strings.Builder
	lower, lq := strings.ToLower(text), strings.ToLower(q)
	for {
		// Lowercasing can change a string's length; only then give up on
		// marking rather than mark the wrong bytes.
		i := strings.Index(lower, lq)
		if i < 0 || len(lower) != len(text) {
			break
		}
		b.WriteString(template.HTMLEscapeString(text[:i]))
		b.WriteString("<mark>" + template.HTMLEscapeString(text[i:i+len(q)]) + "</mark>")
		text, lower = text[i+len(q):], lower[i+len(q):]
	}
	b.WriteString(template.HTMLEscapeString(text))
	return template.HTML(b.String()) //nolint:gosec // escaped
}

// accessText says what an access level lets the caller do.
func accessText(level string) string {
	switch level {
	case access.AdminAccess.String():
		return "You're an admin of this repository."
	case access.ReadWriteAccess.String():
		return "You can read and push to this repository."
	case access.ReadOnlyAccess.String():
		return "You can read this repository."
	}
	return ""
}

// The Atom feed of a history. Links in a feed must be absolute, so they are
// built from the address the reader used, never configured.

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type atomEntry struct {
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Link    atomLink    `xml:"link"`
	Author  atomAuthor  `xml:"author"`
	Content atomContent `xml:"content"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomContent struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

func (h uiHandler) feed(w http.ResponseWriter, r *http.Request, b *browser, p page, path string) {
	cp, err := b.commits(r.Context(), p.Repo.Name, p.Ref, commitFilter{Path: path}, 1, defaultPerPage)
	if err != nil {
		hdr := w.Header()
		hdr.Set("Content-Type", "text/plain; charset=utf-8")
		hdr.Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(statusFor(err))
		w.Write([]byte(publicMessage(err) + "\n")) //nolint:errcheck
		return
	}
	base := baseURL(r)
	title := p.Repo.Name + " commits on " + cp.Ref
	if cp.Path != "" {
		title = p.Repo.Name + " commits to " + cp.Path + " on " + cp.Ref
	}
	f := atomFeed{
		Title: title,
		ID:    base + repoURL(p.Repo.Name, "commits", cp.Ref, cp.Path),
		Links: []atomLink{
			{Rel: "self", Type: "application/atom+xml", Href: base + feedURL(p.Repo.Name, cp.Ref, cp.Path)},
			{Rel: "alternate", Type: "text/html", Href: base + repoURL(p.Repo.Name, "commits", cp.Ref, cp.Path)},
		},
		Updated: time.Unix(0, 0).UTC().Format(time.RFC3339),
	}
	for i, c := range cp.Commits {
		when := c.Committer.When.UTC().Format(time.RFC3339)
		if i == 0 {
			f.Updated = when
		}
		link := base + "/" + p.Repo.Name + "/-/commit/" + c.SHA
		f.Entries = append(f.Entries, atomEntry{
			Title:   c.Summary,
			ID:      link,
			Updated: when,
			Link:    atomLink{Rel: "alternate", Type: "text/html", Href: link},
			Author:  atomAuthor{Name: c.Author.Name},
			Content: atomContent{Type: "text", Body: c.Message},
		})
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(f); err != nil {
		log.FromContext(r.Context()).Error("web: feed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "application/atom+xml; charset=utf-8")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	hdr.Set("Cache-Control", "no-cache")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// feedURL is the Atom feed of a history.
func feedURL(name, ref, p string) string {
	return repoURL(name, "commits.atom", ref, p)
}

// archiveURL is a snapshot download of ref.
func archiveURL(name, ref, format string) string {
	q := url.Values{}
	if ref != "" {
		q.Set("ref", ref)
	}
	if format != "" {
		q.Set("format", format)
	}
	u := "/" + name + "/-/archive"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// compareURL compares head against base (the default branch when empty).
func compareURL(name, base, head string) string {
	q := url.Values{"head": {head}}
	if base != "" {
		q.Set("base", base)
	}
	return "/" + name + "/-/compare?" + q.Encode()
}
