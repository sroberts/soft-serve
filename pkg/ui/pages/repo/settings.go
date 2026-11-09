package repo

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
	"github.com/charmbracelet/soft-serve/pkg/ui/components/form"
)

// settingRow is a line of the settings tab: a setting and its value, or an
// action. act is nil for something the caller cannot change.
type settingRow struct {
	label, value string
	act          func() tea.Cmd
}

// Settings is the tab for a repository's settings, the `repo` setting
// commands over SSH: what it is called, who can see it, whether it mirrors,
// its default branch, and renaming, syncing and deleting it.
type Settings struct {
	common common.Common
	repo   proto.Repository
	info   *ops.Repo
	err    error
	rows   []settingRow
	list   rowList
	host   form.Host
	flash  string
}

// NewSettings returns the settings tab.
func NewSettings(c common.Common) *Settings {
	return &Settings{common: c}
}

// TabName implements common.TabComponent.
func (s *Settings) TabName() string { return "Settings" }

// Path implements common.TabComponent.
func (s *Settings) Path() string { return "" }

// SpinnerID implements common.TabComponent.
func (s *Settings) SpinnerID() int { return -1 }

// StatusBarValue implements common.TabComponent.
func (s *Settings) StatusBarValue() string {
	if s.info == nil {
		return ""
	}
	return s.info.Access
}

// StatusBarInfo implements common.TabComponent.
func (s *Settings) StatusBarInfo() string { return "" }

// SetSize implements common.Component.
func (s *Settings) SetSize(width, height int) { s.common.SetSize(width, height) }

// CapturingInput implements common.InputCapturer.
func (s *Settings) CapturingInput() bool { return s.host.Active() }

// Init implements tea.Model.
func (s *Settings) Init() tea.Cmd { return nil }

func (s *Settings) writable() bool {
	return s.info != nil && canWrite(s.info.Access)
}

// ShortHelp implements help.KeyMap.
func (s *Settings) ShortHelp() []key.Binding {
	if s.host.Active() {
		return s.host.ShortHelp()
	}
	if !s.writable() {
		return nil
	}
	return []key.Binding{s.common.KeyMap.UpDown, binding("enter", "change")}
}

// FullHelp implements help.KeyMap.
func (s *Settings) FullHelp() [][]key.Binding {
	return [][]key.Binding{s.ShortHelp()}
}

func onOff(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (s *Settings) build() {
	s.rows = nil
	if s.info == nil {
		s.list.set(nil)
		return
	}
	i := s.info
	w := s.writable()
	act := func(f func() tea.Cmd) func() tea.Cmd {
		if !w {
			return nil
		}
		return f
	}
	add := func(label, value string, f func() tea.Cmd) {
		s.rows = append(s.rows, settingRow{label: label, value: value, act: act(f)})
	}

	add("Name", i.Name, s.rename)
	add("Project name", i.ProjectName, s.text("project_name", "Project name", i.ProjectName,
		func(v string) ops.RepoUpdate { return ops.RepoUpdate{ProjectName: &v} }))
	add("Description", i.Description, s.text("description", "Description", i.Description,
		func(v string) ops.RepoUpdate { return ops.RepoUpdate{Description: &v} }))
	add("Private", onOff(i.Private), s.toggle(func(b bool) ops.RepoUpdate { return ops.RepoUpdate{Private: &b} }, !i.Private))
	add("Hidden", onOff(i.Hidden), s.toggle(func(b bool) ops.RepoUpdate { return ops.RepoUpdate{Hidden: &b} }, !i.Hidden))
	add("Mirror", onOff(i.Mirror), s.toggle(func(b bool) ops.RepoUpdate { return ops.RepoUpdate{Mirror: &b} }, !i.Mirror))
	if i.AutoSync != nil {
		add("Auto sync", onOff(*i.AutoSync), s.toggle(func(b bool) ops.RepoUpdate { return ops.RepoUpdate{AutoSync: &b} }, !*i.AutoSync))
	}
	if i.DefaultBranch != "" {
		add("Default branch", i.DefaultBranch, s.defaultBranch)
	} else {
		s.rows = append(s.rows, settingRow{label: "Default branch", value: "(no commits yet)"})
	}
	if i.Owner != "" {
		s.rows = append(s.rows, settingRow{label: "Owner", value: i.Owner})
	}
	s.rows = append(s.rows, settingRow{label: "Your access", value: i.Access})
	if w {
		if i.Mirror {
			add("Sync now", "pull from the upstream", s.sync)
		}
		add("Delete", "delete this repository", s.delete)
	}

	lines := make([]string, len(s.rows))
	width := 0
	for _, r := range s.rows {
		width = max(width, len(r.label))
	}
	for j, r := range s.rows {
		lines[j] = fmt.Sprintf("%-*s  %s", width, r.label, r.value)
	}
	s.list.set(lines)
}

// update applies u and opens the repository again to show it.
func (s *Settings) update(u ops.RepoUpdate) error {
	return s.common.Service.UpdateRepo(s.common.Context(), s.info.Name, u)
}

func (s *Settings) saved(msg string) func() tea.Cmd {
	return func() tea.Cmd {
		s.flash = msg
		return reopenCmd(s.info.Name)
	}
}

func (s *Settings) text(key, label, value string, change func(string) ops.RepoUpdate) func() tea.Cmd {
	return func() tea.Cmd {
		f := form.New(s.common, "Change "+strings.ToLower(label), form.Text(key, label, value))
		return s.host.Open(f, func(f *form.Form) error { return s.update(change(f.String(key))) }, s.saved(label+" saved"))
	}
}

func (s *Settings) toggle(change func(bool) ops.RepoUpdate, to bool) func() tea.Cmd {
	return func() tea.Cmd {
		return s.host.Run(func() error { return s.update(change(to)) }, s.saved("Saved"))
	}
}

func (s *Settings) defaultBranch() tea.Cmd {
	f := form.New(s.common, "Default branch",
		form.Choice("branch", "Branch", s.info.Branches, s.info.DefaultBranch))
	return s.host.Open(f, func(f *form.Form) error {
		b := f.Choice("branch")
		return s.update(ops.RepoUpdate{DefaultBranch: &b})
	}, s.saved("Default branch saved"))
}

func (s *Settings) rename() tea.Cmd {
	f := form.New(s.common, "Rename "+s.info.Name, form.Text("name", "New name", s.info.Name))
	var to string
	return s.host.Open(f, func(f *form.Form) error {
		to = f.String("name")
		return s.common.Service.RenameRepo(s.common.Context(), s.info.Name, to)
	}, func() tea.Cmd {
		s.flash = "Renamed"
		return reopenCmd(to)
	})
}

func (s *Settings) sync() tea.Cmd {
	s.flash = "Syncing…"
	return s.host.Run(func() error {
		return s.common.Service.SyncMirror(s.common.Context(), s.info.Name)
	}, s.saved("Synced"))
}

func (s *Settings) delete() tea.Cmd {
	f := form.Confirm(s.common, fmt.Sprintf("Delete %s? This cannot be undone.", s.info.Name))
	return s.host.Open(f, func(*form.Form) error {
		return s.common.Service.DeleteRepo(s.common.Context(), s.info.Name)
	}, func() tea.Cmd { return homeCmd })
}

// Update implements tea.Model.
func (s *Settings) Update(msg tea.Msg) (common.Model, tea.Cmd) {
	if handled, cmd := s.host.Update(msg); handled {
		if s.host.Err() != nil {
			s.flash = ""
		}
		return s, cmd
	}
	switch msg := msg.(type) {
	case RepoMsg:
		if s.repo == nil || msg == nil || s.repo.Name() != proto.Repository(msg).Name() {
			s.flash = ""
			s.list.cursor = 0
		}
		s.repo = msg
		s.info, s.err = nil, nil
		s.host.Close()
		s.build()
	case InfoMsg:
		if s.repo == nil || msg.Name != s.repo.Name() {
			break
		}
		if msg.Err != nil {
			s.err = msg.Err
			break
		}
		info := msg.Info
		s.info = &info
		s.build()
	case tea.KeyPressMsg:
		s.host.ClearErr()
		if s.list.update(s.common, msg) {
			s.flash = ""
			break
		}
		if key.Matches(msg, s.common.KeyMap.Select) && s.list.cursor < len(s.rows) {
			if act := s.rows[s.list.cursor].act; act != nil && !s.host.Busy() {
				s.flash = ""
				return s, act()
			}
		}
	}
	return s, nil
}

// View implements tea.Model.
func (s *Settings) View() string {
	st := s.common.Styles.Form
	if s.host.Active() {
		return s.host.View()
	}
	if s.err != nil {
		return st.Error.Render(s.err.Error())
	}
	if s.info == nil {
		return st.Hint.Render("loading…")
	}
	var foot string
	switch {
	case s.host.Err() != nil:
		foot = st.Error.Render(s.host.Err().Error())
	case s.host.Busy():
		foot = st.Hint.Render("working…")
	case s.flash != "":
		foot = st.Success.Render(s.flash)
	case !s.writable():
		foot = st.Hint.Render("You have " + s.info.Access + " access, so these can't be changed here.")
	}
	return s.list.view(s.common, s.common.Height-2) + "\n" + foot
}
