package web

import (
	"html/template"

	"github.com/charmbracelet/soft-serve/pkg/issue"
	"github.com/charmbracelet/soft-serve/pkg/ops"
)

// icons are the pages' line icons: drawn on a 16-unit grid with a 1.5
// stroke and round ends, in currentColor, to sit beside the issue views'
// status and priority icons. Each names a thing the page already says in
// words, so every icon is hidden from screen readers.
var icons = map[string]string{
	// Files.
	"folder":    `<path d="M1.75 4a1 1 0 0 1 1-1h3.4l1.6 1.75h5.5a1 1 0 0 1 1 1v6.5a1 1 0 0 1-1 1H2.75a1 1 0 0 1-1-1z"/>`,
	"file":      `<path d="M4 1.75h4.75L12.5 5.5v7.75a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V2.75a1 1 0 0 1 1-1z"/><path d="M8.75 1.75V5.5h3.75"/>`,
	"symlink":   `<path d="M4 1.75h4.75L12.5 5.5v7.75a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V2.75a1 1 0 0 1 1-1z"/><path d="M8.75 1.75V5.5h3.75"/><path d="M5.75 12c0-1.9 1.1-2.75 3.25-2.75"/><path d="M7.75 7.75 9.25 9.25l-1.5 1.5"/>`,
	"submodule": `<path d="M8 1.75 13.5 4.6v6.8L8 14.25 2.5 11.4V4.6z"/><path d="M2.5 4.6 8 7.5l5.5-2.9M8 7.5v6.75"/>`,
	"up":        `<path d="M13 13H7.25a2 2 0 0 1-2-2V3"/><path d="M2.5 5.75 5.25 3 8 5.75"/>`,
	// Refs and history.
	"branch":   `<circle cx="4.75" cy="3.5" r="1.5"/><circle cx="4.75" cy="12.5" r="1.5"/><circle cx="11.25" cy="4.5" r="1.5"/><path d="M4.75 5v6"/><path d="M11.25 6c0 3.25-6.5 2.25-6.5 5"/>`,
	"tag":      `<path d="M1.75 2.75a1 1 0 0 1 1-1h4.6l6.35 6.35a1 1 0 0 1 0 1.4l-4.2 4.2a1 1 0 0 1-1.4 0L1.75 7.35z"/><circle cx="5" cy="5" r=".9" fill="currentColor" stroke="none"/>`,
	"commit":   `<circle cx="8" cy="8" r="2.5"/><path d="M1.5 8h4M10.5 8h4"/>`,
	"download": `<path d="M8 2v8"/><path d="M4.75 6.75 8 10l3.25-3.25"/><path d="M2.5 13.25h11"/>`,
	// Repositories.
	"repo":   `<path d="M3.25 12.75V3a1.25 1.25 0 0 1 1.25-1.25h8.25v10H4.5a1.25 1.25 0 0 0 0 2.5h8.25"/><path d="M6 1.75v4.5l1.5-1 1.5 1v-4.5"/>`,
	"plus":   `<path d="M8 3.25v9.5M3.25 8h9.5"/>`,
	"lock":   `<rect x="3" y="7" width="10" height="7" rx="1.5"/><path d="M5.25 7V5a2.75 2.75 0 0 1 5.5 0v2"/>`,
	"mirror": `<path d="M2.5 5.75h10.5L10.5 3.25"/><path d="M13.5 10.25H3l2.5 2.5"/>`,
	// Signatures.
	"verified":   `<path d="M8 1.5 13.25 3.5v4c0 3.15-2.2 5.6-5.25 7-3.05-1.4-5.25-3.85-5.25-7v-4z"/><path d="M5.6 8 7.3 9.7l3.1-3.3"/>`,
	"unverified": `<path d="M8 1.5 13.25 3.5v4c0 3.15-2.2 5.6-5.25 7-3.05-1.4-5.25-3.85-5.25-7v-4z"/><path d="M6.25 6.25l3.5 3.5M9.75 6.25l-3.5 3.5"/>`,
	"check":      `<path d="M3 8.5 6.25 11.5 13 4.75"/>`,
	// Issue activity.
	"opened":  `<circle cx="8" cy="8" r="5.75"/><path d="M8 5.5v5M5.5 8h5"/>`,
	"person":  `<circle cx="8" cy="5.25" r="2.75"/><path d="M2.75 14c.6-2.6 2.7-4 5.25-4s4.65 1.4 5.25 4"/>`,
	"pencil":  `<path d="M10.75 2.25 13.75 5.25 5.5 13.5H2.5v-3z"/><path d="M9 4l3 3"/>`,
	"parent":  `<path d="M4 2v7a2 2 0 0 0 2 2h7"/><path d="M10.5 8.5 13 11l-2.5 2.5"/>`,
	"project": `<path d="M3.75 14.25V2.25"/><path d="M3.75 2.75h8.5l-2 3 2 3h-8.5"/>`,
	"cycle":   `<path d="M13.25 8a5.25 5.25 0 0 1-9.4 3.2"/><path d="M2.75 8a5.25 5.25 0 0 1 9.4-3.2"/><path d="M12.5 1.75V5H9.25"/><path d="M3.5 14.25V11h3.25"/>`,
	// The repository tabs and the issue views.
	"code":     `<path d="M5.5 4.25 1.75 8l3.75 3.75M10.5 4.25 14.25 8 10.5 11.75"/>`,
	"issue":    `<circle cx="8" cy="8" r="5.75"/><circle cx="8" cy="8" r="1.5" fill="currentColor" stroke="none"/>`,
	"compare":  `<path d="M5 2.5v8.75M2.75 9 5 11.25 7.25 9"/><path d="M11 13.5V4.75M8.75 7 11 4.75 13.25 7"/>`,
	"search":   `<circle cx="7" cy="7" r="4.5"/><path d="M10.5 10.5 13.75 13.75"/>`,
	"insights": `<path d="M2.25 13.75h11.5"/><path d="M4.75 11V8.25M8 11V3.75M11.25 11V6.25"/>`,
	"settings": `<path d="M2.25 4.75h6.25M12.75 4.75h1M2.25 11.25h1.5M7.5 11.25h6.25"/><circle cx="10.5" cy="4.75" r="1.75"/><circle cx="5.5" cy="11.25" r="1.75"/>`,
	"list":     `<path d="M6 4h7.75M6 8h7.75M6 12h7.75"/><circle cx="2.75" cy="4" r=".9" fill="currentColor" stroke="none"/><circle cx="2.75" cy="8" r=".9" fill="currentColor" stroke="none"/><circle cx="2.75" cy="12" r=".9" fill="currentColor" stroke="none"/>`,
	"board":    `<rect x="1.75" y="2.25" width="12.5" height="11.5" rx="2"/><path d="M6 2.25v11.5M10 2.25v11.5"/>`,
	// Diffs: how a file changed, and the fold on each file.
	"diff-added":    `<rect x="2.25" y="2.25" width="11.5" height="11.5" rx="2.5"/><path d="M8 5.25v5.5M5.25 8h5.5"/>`,
	"diff-modified": `<rect x="2.25" y="2.25" width="11.5" height="11.5" rx="2.5"/><circle cx="8" cy="8" r="1.75" fill="currentColor" stroke="none"/>`,
	"diff-deleted":  `<rect x="2.25" y="2.25" width="11.5" height="11.5" rx="2.5"/><path d="M5.25 8h5.5"/>`,
	"diff-renamed":  `<rect x="2.25" y="2.25" width="11.5" height="11.5" rx="2.5"/><path d="M5.25 8h5.5M8.5 5.75 10.75 8 8.5 10.25"/>`,
	"chevron":       `<path d="M6 4l4 4-4 4"/>`,
	// Actions and messages.
	"copy":  `<rect x="5.25" y="5.25" width="8.5" height="8.5" rx="1.5"/><path d="M10.75 5.25V3.75a1.5 1.5 0 0 0-1.5-1.5h-5.5a1.5 1.5 0 0 0-1.5 1.5v5.5a1.5 1.5 0 0 0 1.5 1.5h1.5"/>`,
	"done":  `<circle cx="8" cy="8" r="6.25"/><path d="M5.4 8.2 7.2 10l3.4-3.7"/>`,
	"alert": `<circle cx="8" cy="8" r="6.25"/><path d="M8 4.75v3.75"/><circle cx="8" cy="11" r=".9" fill="currentColor" stroke="none"/>`,
	"feed":  `<path d="M3 2.75a10.25 10.25 0 0 1 10.25 10.25"/><path d="M3 7a6 6 0 0 1 6 6"/><circle cx="3.75" cy="12.25" r="1.25" fill="currentColor" stroke="none"/>`,
}

// diffIcon draws how a file changed: added, modified, deleted or renamed.
func diffIcon(status string) template.HTML {
	return icon("diff-" + status)
}

// refIcon marks what the page is showing: a branch, a tag, or (for
// anything else, such as a commit hash) a commit.
func refIcon(m *RefMenu) template.HTML {
	switch {
	case m == nil:
		return ""
	case m.CurrentIsBranch:
		return icon("branch")
	case m.CurrentIsTag:
		return icon("tag")
	}
	return icon("commit")
}

// icon draws the named icon, or nothing for a name it doesn't know.
func icon(name string) template.HTML {
	body, ok := icons[name]
	if !ok {
		return ""
	}
	return template.HTML(`<svg class="ic ic-` + name + `" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`) //nolint:gosec // fixed markup from the table above
}

// entryIcon draws a tree entry by its type.
func entryIcon(typ string) template.HTML {
	switch typ {
	case "dir":
		return icon("folder")
	case "submodule", "symlink":
		return icon(typ)
	}
	return icon("file")
}

// eventIcon marks an activity entry with what it changed: the new status or
// priority itself, or the thing it touched.
func eventIcon(e ops.IssueEvent) template.HTML {
	switch e.Kind {
	case issue.EventStatus:
		return statusIcon(e.To)
	case issue.EventPriority:
		return priorityIcon(e.To)
	case issue.EventAssignee:
		return icon("person")
	case issue.EventTitle, issue.EventDescription:
		return icon("pencil")
	case issue.EventLabelAdded, issue.EventLabelRemove:
		return icon("tag")
	case issue.EventParent:
		return icon("parent")
	case issue.EventProject:
		return icon("project")
	case issue.EventCycle:
		return icon("cycle")
	case issue.EventBranch:
		return icon("branch")
	case issue.EventCommit:
		return icon("commit")
	}
	return icon("opened")
}
