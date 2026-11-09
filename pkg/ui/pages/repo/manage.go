package repo

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
)

// InfoMsg is what the caller may see and do with the open repository, as
// the Service reports it. The repository page fetches it after every RepoMsg
// when there is a Service, and hands it to every tab.
type InfoMsg struct {
	Name string
	Info ops.Repo
	Err  error
}

func loadInfoCmd(c common.Common, name string) tea.Cmd {
	if c.Service == nil {
		return nil
	}
	return func() tea.Msg {
		info, err := c.Service.Repo(c.Context(), name)
		return InfoMsg{Name: name, Info: info, Err: err}
	}
}

// canWrite reports whether access lets the caller change the repository.
func canWrite(access string) bool {
	return access == "read-write" || access == "admin-access"
}

// isAdmin reports whether access lets the caller manage webhooks.
func isAdmin(access string) bool {
	return access == "admin-access"
}

func reopenCmd(name string) tea.Cmd {
	return func() tea.Msg { return common.OpenRepoMsg(name) }
}

func homeCmd() tea.Msg { return common.HomeMsg{} }

// rowList is a plain list with a cursor, for the management tabs.
type rowList struct {
	rows   []string
	cursor int
	offset int
}

func (l *rowList) set(rows []string) {
	l.rows = rows
	if l.cursor >= len(rows) {
		l.cursor = max(0, len(rows)-1)
	}
}

// update moves the cursor for up, down, top and bottom keys, and reports
// whether the key was one of them.
func (l *rowList) update(c common.Common, k tea.KeyPressMsg) bool {
	switch {
	case key.Matches(k, c.KeyMap.Up):
		if l.cursor > 0 {
			l.cursor--
		}
	case key.Matches(k, c.KeyMap.Down):
		if l.cursor < len(l.rows)-1 {
			l.cursor++
		}
	case key.Matches(k, c.KeyMap.GotoTop):
		l.cursor = 0
	case key.Matches(k, c.KeyMap.GotoBottom):
		l.cursor = max(0, len(l.rows)-1)
	default:
		return false
	}
	return true
}

func (l *rowList) view(c common.Common, height int) string {
	if height < 1 {
		height = 1
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+height {
		l.offset = l.cursor - height + 1
	}
	var b strings.Builder
	for i := l.offset; i < len(l.rows) && i < l.offset+height; i++ {
		row := common.TruncateString(l.rows[i], max(10, c.Width-4))
		if i == l.cursor {
			b.WriteString(c.Styles.Form.RowSelected.Render(row))
		} else {
			b.WriteString(c.Styles.Form.Row.Render(row))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// binding is a help entry for a management key.
func binding(keys, help string) key.Binding {
	return key.NewBinding(key.WithKeys(strings.Split(keys, "/")...), key.WithHelp(keys, help))
}
