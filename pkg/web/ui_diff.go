package web

import (
	"net/url"
	"strconv"
	"strings"
)

// Diffs on the commit and compare pages: one collapsible section per file,
// shown unified or side by side, with or without whitespace changes. The
// patch is cut at maxPatchBytes and each file at 1,000 lines; a file cut
// short, or left out, links to its diff alone, which is shown in full.

// diffLine is one line of a unified diff.
type diffLine struct {
	Kind string // add, del, ctx, hunk or note
	Old  int    // line number before, 0 for none
	New  int    // line number after, 0 for none
	Text string // without its +, - or space
}

// splitRow is one row of a side-by-side diff: a hunk header, or a line
// before and a line after, either of which may be missing.
type splitRow struct {
	Hunk  string
	Left  *diffLine
	Right *diffLine
}

// fileDiff is one file's part of a diff.
type fileDiff struct {
	FileChange
	ID    string
	Shown bool // some of its patch is here
	Cut   bool // its patch was cut short
	Lines []diffLine
	Split []splitRow
	// FullURL shows this file's diff alone, and in full.
	FullURL string
}

// diffView is how a diff is shown, and the links that change it.
type diffView struct {
	Split            bool
	IgnoreWhitespace bool
	Path             string
	UnifiedURL       string
	SplitURL         string
	WhitespaceURL    string // toggles IgnoreWhitespace
	AllFilesURL      string // leaves Path
	Files            []fileDiff
	PatchTruncated   bool
}

// newDiffView lays out a patch for files. base is the page's own URL path,
// and q its query, which the view's links keep.
func newDiffView(base string, q url.Values, files []FileChange, patch string, truncated bool) diffView {
	v := diffView{
		Split:            q.Get("view") == "split",
		IgnoreWhitespace: q.Get("w") == "1",
		Path:             q.Get("path"),
		PatchTruncated:   truncated,
	}
	with := func(key, val string) string {
		nq := url.Values{}
		for k, vs := range q {
			nq[k] = vs
		}
		if val == "" {
			nq.Del(key)
		} else {
			nq.Set(key, val)
		}
		if len(nq) == 0 {
			return base
		}
		return base + "?" + nq.Encode()
	}
	v.UnifiedURL = with("view", "")
	v.SplitURL = with("view", "split")
	if v.IgnoreWhitespace {
		v.WhitespaceURL = with("w", "")
	} else {
		v.WhitespaceURL = with("w", "1")
	}
	v.AllFilesURL = with("path", "")

	chunks := splitPatch(patch)
	for i, f := range files {
		fd := fileDiff{FileChange: f, ID: "diff-" + strconv.Itoa(i+1), FullURL: with("path", f.Path)}
		if i < len(chunks) {
			fd.Shown = true
			fd.Lines = parseChunk(chunks[i])
			// The last chunk of a cut patch is cut too.
			fd.Cut = f.Truncated || (truncated && i == len(chunks)-1)
			if v.Split {
				fd.Split = pairLines(fd.Lines)
			}
		}
		v.Files = append(v.Files, fd)
	}
	return v
}

// splitPatch cuts a patch into each file's part, in order.
func splitPatch(patch string) []string {
	if patch == "" {
		return nil
	}
	var chunks []string
	start := -1
	for i := 0; i < len(patch); {
		end := strings.IndexByte(patch[i:], '\n')
		if end < 0 {
			end = len(patch) - i
		} else {
			end++
		}
		if strings.HasPrefix(patch[i:], "diff --git ") {
			if start >= 0 {
				chunks = append(chunks, patch[start:i])
			}
			start = i
		}
		i += end
	}
	if start >= 0 {
		chunks = append(chunks, patch[start:])
	}
	return chunks
}

// parseChunk reads one file's patch into lines, numbered from its hunk
// headers. The file header (diff, index, ---, +++ and the like) is left
// out: the page names the file already.
func parseChunk(chunk string) []diffLine {
	var out []diffLine
	old, nw := 0, 0
	inHunk := false
	for _, l := range strings.Split(strings.TrimSuffix(chunk, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "@@"):
			inHunk = true
			old, nw = hunkStart(l)
			out = append(out, diffLine{Kind: "hunk", Text: l})
		case !inHunk:
			// File header.
		case strings.HasPrefix(l, "+"):
			out = append(out, diffLine{Kind: "add", New: nw, Text: l[1:]})
			nw++
		case strings.HasPrefix(l, "-"):
			out = append(out, diffLine{Kind: "del", Old: old, Text: l[1:]})
			old++
		case strings.HasPrefix(l, `\`):
			out = append(out, diffLine{Kind: "note", Text: l})
		default:
			out = append(out, diffLine{Kind: "ctx", Old: old, New: nw, Text: strings.TrimPrefix(l, " ")})
			old++
			nw++
		}
	}
	return out
}

// hunkStart reads the first old and new line numbers from "@@ -a,b +c,d @@".
func hunkStart(h string) (int, int) {
	f := strings.Fields(h)
	if len(f) < 3 {
		return 0, 0
	}
	num := func(s string) int {
		s = strings.TrimLeft(s, "-+")
		s, _, _ = strings.Cut(s, ",")
		n, _ := strconv.Atoi(s)
		return n
	}
	return num(f[1]), num(f[2])
}

// pairLines lays unified lines side by side: a run of deletions beside the
// run of additions that follows it.
func pairLines(lines []diffLine) []splitRow {
	var out []splitRow
	var dels, adds []*diffLine
	flush := func() {
		for i := range max(len(dels), len(adds)) {
			var r splitRow
			if i < len(dels) {
				r.Left = dels[i]
			}
			if i < len(adds) {
				r.Right = adds[i]
			}
			out = append(out, r)
		}
		dels, adds = nil, nil
	}
	for i := range lines {
		l := &lines[i]
		switch l.Kind {
		case "del":
			if len(adds) > 0 {
				flush()
			}
			dels = append(dels, l)
		case "add":
			adds = append(adds, l)
		case "hunk":
			flush()
			out = append(out, splitRow{Hunk: l.Text})
		default:
			flush()
			out = append(out, splitRow{Left: l, Right: l})
		}
	}
	flush()
	return out
}
