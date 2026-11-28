package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/issue"
	"github.com/charmbracelet/soft-serve/pkg/ops"
)

// TestIconsExist checks that every icon a template names is drawn, so a
// typo can't leave a silent gap where an icon should be.
func TestIconsExist(t *testing.T) {
	named := regexp.MustCompile(`\{\{icon "([a-z-]+)"\}\}`)
	files, err := fs.Glob(uiFS, "ui_templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, f := range files {
		b, err := fs.ReadFile(uiFS, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range named.FindAllStringSubmatch(string(b), -1) {
			seen++
			if _, ok := icons[m[1]]; !ok {
				t.Errorf("%s uses icon %q, which isn't drawn", f, m[1])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no icons found in the templates")
	}
	if icon("nope") != "" {
		t.Error("an unknown icon should draw nothing")
	}
	for _, typ := range []string{"dir", "file", "symlink", "submodule"} {
		if !strings.Contains(string(entryIcon(typ)), "<svg") {
			t.Errorf("no icon for a %s", typ)
		}
	}
	for _, k := range []string{issue.EventCreated, issue.EventTitle, issue.EventDescription, issue.EventStatus, issue.EventPriority, issue.EventAssignee, issue.EventLabelAdded, issue.EventLabelRemove, issue.EventParent, issue.EventProject, issue.EventCycle, issue.EventCommit, issue.EventBranch} {
		e := ops.IssueEvent{Kind: k, To: "todo"}
		if k == issue.EventPriority {
			e.To = "high"
		}
		got := string(eventIcon(e))
		if !strings.Contains(got, `aria-hidden="true"`) {
			t.Errorf("no hidden icon for a %s event", k)
		}
		// Anything but a creation falling through to the opened mark means
		// eventIcon has no case for it.
		if k != issue.EventCreated && strings.Contains(got, "ic-opened") {
			t.Errorf("a %s event falls back to the opened icon", k)
		}
	}
	for _, st := range []string{"added", "modified", "deleted", "renamed"} {
		if diffIcon(st) == "" {
			t.Errorf("no icon for a %s file", st)
		}
	}
	for _, c := range []struct {
		m    *RefMenu
		want string
	}{
		{nil, ""},
		{&RefMenu{CurrentIsBranch: true}, "ic-branch"},
		{&RefMenu{CurrentIsTag: true}, "ic-tag"},
		{&RefMenu{CurrentIsBranch: true, CurrentIsTag: true}, "ic-tag"}, // git reads the tag
		{&RefMenu{Current: "0123456789abcdef0123456789abcdef01234567"}, "ic-commit"},
	} {
		got := string(refIcon(c.m))
		if (c.want == "" && got != "") || !strings.Contains(got, c.want) {
			t.Errorf("refIcon(%+v) = %q, want %s", c.m, got, c.want)
		}
	}
}

// TestCopyScriptServed checks the copy buttons' script is one the server
// hands out, not an empty file under a hash.
func TestCopyScriptServed(t *testing.T) {
	if len(staticBody("copy.js")) == 0 {
		t.Fatal("copy.js isn't served")
	}
}

// TestCopyTargetsAdjacent checks every copy button sits right after the
// element it copies, since copy.js copies its neighbour and only when that
// neighbour carries the id the button names.
func TestCopyTargetsAdjacent(t *testing.T) {
	button := regexp.MustCompile(`data-copy="([a-z-]+)"`)
	files, err := fs.Glob(uiFS, "ui_templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, f := range files {
		b, err := fs.ReadFile(uiFS, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range button.FindAllStringSubmatch(string(b), -1) {
			seen++
			adjacent := regexp.MustCompile(`id="` + m[1] + `">[^<]*</code><button type="button" class="copybtn" data-copy="` + m[1] + `"`)
			if !adjacent.Match(b) {
				t.Errorf("%s: the copy button for %q doesn't follow its target", f, m[1])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no copy buttons found")
	}
}

// TestSiteMark checks the mark beside the site name and the favicon: the
// page links both, they're served, and /favicon.ico answers with an image
// rather than a not-found page.
func TestSiteMark(t *testing.T) {
	f := newViewFixture(t)
	home := f.do(req{path: "/"}).Body.String()
	for _, want := range []string{`<img class="mark" src="/-/static/mark.svg?v=`, `rel="icon" href="/-/static/mark.svg?v=`, `rel="icon" href="/-/static/favicon-32.png?v=`} {
		if !strings.Contains(home, want) {
			t.Errorf("home page has no %s", want)
		}
	}
	svg := f.do(req{path: "/-/static/mark.svg"})
	if svg.Code != 200 || svg.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("mark.svg: %d %q", svg.Code, svg.Header().Get("Content-Type"))
	}
	ico := f.do(req{path: "/favicon.ico"})
	if ico.Code != 200 || ico.Header().Get("Content-Type") != "image/png" || ico.Body.Len() == 0 {
		t.Errorf("favicon.ico: %d %q, %d bytes", ico.Code, ico.Header().Get("Content-Type"), ico.Body.Len())
	}
}
