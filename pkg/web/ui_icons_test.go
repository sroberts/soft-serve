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
		if !strings.Contains(string(eventIcon(e)), `aria-hidden="true"`) {
			t.Errorf("no hidden icon for a %s event", k)
		}
	}
}
