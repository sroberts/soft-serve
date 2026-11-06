package web

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	gopath "path"
	"strconv"
	"strings"
	"time"
)

// Pages for finding files, a repository's insights and your account, and
// the helpers the other pages use for line links, history filters and
// branch distances.

// --- Lines of a file ---

// lineSel is a selected run of lines, from 1; zero when none.
type lineSel struct{ Start, End int }

// parseLines reads ?lines=10 or ?lines=10-25.
func parseLines(s string) lineSel {
	a, b, isRange := strings.Cut(s, "-")
	start, err := strconv.Atoi(a)
	if err != nil || start < 1 {
		return lineSel{}
	}
	end := start
	if isRange {
		if end, err = strconv.Atoi(b); err != nil || end < 1 {
			return lineSel{}
		}
	}
	if end < start {
		start, end = end, start
	}
	return lineSel{start, end}
}

func (s lineSel) String() string {
	if s.Start == 0 {
		return ""
	}
	if s.End == s.Start {
		return strconv.Itoa(s.Start)
	}
	return strconv.Itoa(s.Start) + "-" + strconv.Itoa(s.End)
}

// Has says whether line n is selected.
func (s lineSel) Has(n int) bool { return s.Start > 0 && n >= s.Start && n <= s.End }

// next is the selection a click on line n makes: with one line selected,
// the run from it to n; otherwise just n. A page with no script can still
// select a range: click its first line, then its last.
func (s lineSel) next(n int) lineSel {
	if s.Start > 0 && s.Start == s.End && n != s.Start {
		return lineSel{min(s.Start, n), max(s.Start, n)}
	}
	return lineSel{n, n}
}

// linesURL is a file page with lines selected and scrolled to.
func linesURL(name, action, ref, p string, sel lineSel) string {
	u := repoURL(name, action, ref, p)
	if sel.Start == 0 {
		return u
	}
	return u + "&lines=" + sel.String() + "#L" + strconv.Itoa(sel.Start)
}

// lineURL is a file page with line n selected.
func lineURL(name, ref, p string, n int) string {
	return linesURL(name, "blob", ref, p, lineSel{n, n})
}

// codeLine is one line of a file page.
type codeLine struct {
	N        int
	Code     template.HTML
	Href     string
	Selected bool
}

// codeLines numbers a file's highlighted lines and links each to the
// selection a click on it makes.
func codeLines(name, ref, p, src string, sel lineSel) []codeLine {
	hl := highlightLines(gopath.Base(p), src)
	out := make([]codeLine, len(hl))
	base := repoURL(name, "blob", ref, p) // a blob URL always has ?path=
	for i, h := range hl {
		n := i + 1
		next := sel.next(n)
		href := base + "&lines=" + next.String() + "#L" + strconv.Itoa(next.Start)
		out[i] = codeLine{N: n, Code: h, Selected: sel.Has(n), Href: href}
	}
	return out
}

// --- History filters ---

// commitsURL is a page of a filtered history.
func commitsURL(name string, cp CommitPage, n int) string {
	q := url.Values{}
	if n > 1 {
		q.Set("page", strconv.Itoa(n))
	}
	for k, v := range map[string]string{"ref": cp.Ref, "path": cp.Path, "q": cp.Query, "author": cp.Author} {
		if v != "" {
			q.Set(k, v)
		}
	}
	u := "/" + name + "/-/commits"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// authorURL is the commits by someone, at ref.
func authorURL(name, ref, author string) string {
	return commitsURL(name, CommitPage{Ref: ref, Author: author}, 1)
}

// --- Branches ---

// distanceText says how far a branch is from the default branch.
func distanceText(r RefInfo, def string) string {
	if r.Ahead == nil || r.Behind == nil {
		return ""
	}
	a, b := *r.Ahead, *r.Behind
	switch {
	case a == 0 && b == 0:
		return "Even with " + def
	case b == 0:
		return fmt.Sprintf("%d ahead of %s", a, def)
	case a == 0:
		return fmt.Sprintf("%d behind %s", b, def)
	}
	return fmt.Sprintf("%d ahead, %d behind %s", a, b, def)
}

// agoPtr is ago for a time that may be missing.
func agoPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return ago(*t)
}

// --- Finding files ---

type findRow struct {
	Path   string
	Marked template.HTML
}

type findData struct {
	Query  string
	Result *FileList
	Rows   []findRow
	Err    string
}

func (h uiHandler) find(w http.ResponseWriter, r *http.Request, b *browser, p page, q string) {
	p.Tab = "files"
	p.Title = "Go to file · " + p.Repo.Name
	d := findData{Query: strings.TrimSpace(q)}
	if d.Query == "" || p.Repo.Empty {
		p.Data = d
		h.render(w, r, "find", p)
		return
	}
	res, err := b.findFiles(r.Context(), p.Repo.Name, p.Ref, d.Query)
	switch {
	case err == nil:
	case statusFor(err) == http.StatusInternalServerError:
		h.fail(w, r, p, err)
		return
	default:
		d.Err = publicMessage(err)
		p.Data = d
		h.renderStatus(w, r, statusFor(err), "find", p)
		return
	}
	// One match, or an exact path: go straight to it.
	if res.Total == 1 || (len(res.Files) > 0 && res.Files[0] == strings.Trim(d.Query, "/")) {
		http.Redirect(w, r, repoURL(p.Repo.Name, "blob", p.Ref, res.Files[0]), http.StatusFound)
		return
	}
	p.Ref = res.Ref
	p.Title = d.Query + " · Go to file · " + p.Repo.Name
	d.Result = &res
	for _, f := range res.Files {
		d.Rows = append(d.Rows, findRow{Path: f, Marked: markRuns(f, findMarks(f, d.Query))})
	}
	p.Data = d
	h.render(w, r, "find", p)
}

// markRuns escapes s and marks the byte runs given.
func markRuns(s string, runs [][2]int) template.HTML {
	var b strings.Builder
	at := 0
	for _, r := range runs {
		if r[0] < at || r[1] > len(s) {
			break
		}
		b.WriteString(template.HTMLEscapeString(s[at:r[0]]))
		b.WriteString("<mark>" + template.HTMLEscapeString(s[r[0]:r[1]]) + "</mark>")
		at = r[1]
	}
	b.WriteString(template.HTMLEscapeString(s[at:]))
	return template.HTML(b.String()) //nolint:gosec // escaped
}

// --- Insights ---

// weekBar is one week of the activity chart.
type weekBar struct {
	X, Y, H float64
	Label   string
}

// langRow is one language of the breakdown.
type langRow struct {
	Name    string
	Percent string
	Files   int
	Width   float64 // of the bar, 0 to 100
}

type insightsData struct {
	Stats      Stats
	Bars       []weekBar
	ChartW     float64
	ChartH     float64
	BarW       float64
	YearTotal  int
	BusiestWk  string
	BusiestN   int
	Languages  []langRow
	OtherShare string
}

const (
	chartBarW = 12.0
	chartGap  = 2.0
	chartH    = 72.0
	// maxLanguages are listed; the rest are summed as other.
	maxLanguages = 8
)

func (h uiHandler) insights(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "insights"
	p.Title = "Insights · " + p.Repo.Name
	if p.Repo.Empty {
		h.fail(w, r, p, errEmptyRepo)
		return
	}
	st, err := b.stats(r.Context(), p.Repo.Name, p.Ref)
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Ref = st.Ref
	d := insightsData{Stats: st, ChartH: chartH, BarW: chartBarW, ChartW: float64(len(st.Weekly))*(chartBarW+chartGap) - chartGap}

	peak := 0
	for _, wk := range st.Weekly {
		d.YearTotal += wk.Commits
		if wk.Commits > peak {
			peak = wk.Commits
			d.BusiestN = wk.Commits
			d.BusiestWk = wk.Week.Format("2 Jan 2006")
		}
	}
	for i, wk := range st.Weekly {
		bh := 0.0
		if peak > 0 && wk.Commits > 0 {
			// At least 3px, so a week with one commit still shows.
			bh = math.Max(3, chartH*float64(wk.Commits)/float64(peak))
		}
		plural := "s"
		if wk.Commits == 1 {
			plural = ""
		}
		d.Bars = append(d.Bars, weekBar{
			X:     float64(i) * (chartBarW + chartGap),
			Y:     chartH - bh,
			H:     bh,
			Label: fmt.Sprintf("%d commit%s in the week of %s", wk.Commits, plural, wk.Week.Format("2 Jan 2006")),
		})
	}

	var other float64
	top := 0.0
	if len(st.Languages) > 0 {
		top = st.Languages[0].Percent
	}
	for i, l := range st.Languages {
		if i >= maxLanguages {
			other += l.Percent
			continue
		}
		width := 0.0
		if top > 0 {
			width = l.Percent * 100 / top
		}
		d.Languages = append(d.Languages, langRow{Name: l.Name, Percent: percent(l.Percent), Files: l.Files, Width: width})
	}
	if other > 0 {
		d.OtherShare = percent(other)
	}
	p.Data = d
	h.render(w, r, "insights", p)
}

// percent shows a share to one decimal place, or "<0.1%".
func percent(f float64) string {
	if f > 0 && f < 0.1 {
		return "<0.1%"
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + "%"
}

// --- Your account ---

func (h uiHandler) account(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	acct, err := b.account(r.Context(), p.SignedIn)
	if err != nil {
		h.fail(w, r, p, err)
		return
	}
	p.Title = "Your account"
	if acct.SignedIn {
		p.Title = acct.Username + " · Your account"
	}
	p.Data = acct
	h.render(w, r, "account", p)
}
