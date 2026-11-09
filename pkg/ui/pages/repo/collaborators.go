package repo

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
	"github.com/charmbracelet/soft-serve/pkg/ui/components/form"
)

// accessLevels are the levels a collaborator can be given.
var accessLevels = []string{"read-only", "read-write", "admin-access", "no-access"}

// collabsMsg is a repository's collaborators, loaded.
type collabsMsg struct {
	name    string
	collabs []ops.Collaborator
	err     error
}

// Collaborators is the tab for a repository's collaborators, `repo collab`
// over SSH. It needs write access, as the commands do.
type Collaborators struct {
	common  common.Common
	repo    proto.Repository
	access  string
	collabs []ops.Collaborator
	loaded  bool
	err     error
	list    rowList
	host    form.Host
	flash   string
}

// NewCollaborators returns the collaborators tab.
func NewCollaborators(c common.Common) *Collaborators {
	return &Collaborators{common: c}
}

// TabName implements common.TabComponent.
func (c *Collaborators) TabName() string { return "Collaborators" }

// Path implements common.TabComponent.
func (c *Collaborators) Path() string { return "" }

// SpinnerID implements common.TabComponent.
func (c *Collaborators) SpinnerID() int { return -1 }

// StatusBarValue implements common.TabComponent.
func (c *Collaborators) StatusBarValue() string {
	if c.list.cursor < len(c.collabs) {
		return c.collabs[c.list.cursor].Username
	}
	return ""
}

// StatusBarInfo implements common.TabComponent.
func (c *Collaborators) StatusBarInfo() string {
	return fmt.Sprintf("%d collaborators", len(c.collabs))
}

// SetSize implements common.Component.
func (c *Collaborators) SetSize(width, height int) { c.common.SetSize(width, height) }

// CapturingInput implements common.InputCapturer.
func (c *Collaborators) CapturingInput() bool { return c.host.Active() }

// Init implements tea.Model.
func (c *Collaborators) Init() tea.Cmd { return nil }

// ShortHelp implements help.KeyMap.
func (c *Collaborators) ShortHelp() []key.Binding {
	if c.host.Active() {
		return c.host.ShortHelp()
	}
	if !canWrite(c.access) {
		return nil
	}
	b := []key.Binding{binding("a", "add")}
	if len(c.collabs) > 0 {
		b = append(b, c.common.KeyMap.UpDown, binding("enter", "change access"), binding("x", "remove"))
	}
	return b
}

// FullHelp implements help.KeyMap.
func (c *Collaborators) FullHelp() [][]key.Binding {
	return [][]key.Binding{c.ShortHelp()}
}

func (c *Collaborators) load() tea.Cmd {
	name := c.repo.Name()
	svc, ctx := c.common.Service, c.common.Context()
	return func() tea.Msg {
		cs, err := svc.Collaborators(ctx, name)
		return collabsMsg{name: name, collabs: cs, err: err}
	}
}

func (c *Collaborators) reload(flash string) func() tea.Cmd {
	return func() tea.Cmd {
		c.flash = flash
		return c.load()
	}
}

func (c *Collaborators) add() tea.Cmd {
	f := form.New(c.common, "Add a collaborator",
		form.Text("username", "Username", "").Placeholder("an existing user"),
		form.Choice("access", "Access", accessLevels, "read-write").
			Hint("nobody but a server admin can grant more access than they have"),
	)
	return c.host.Open(f, func(f *form.Form) error {
		return c.common.Service.AddCollaborator(c.common.Context(), c.repo.Name(), f.String("username"), f.Choice("access"))
	}, c.reload("Added"))
}

// change gives a collaborator another level: a removal and an addition,
// under the same rules as each.
func (c *Collaborators) change(who ops.Collaborator) tea.Cmd {
	f := form.New(c.common, "Change "+who.Username+"'s access",
		form.Choice("access", "Access", accessLevels, who.Access))
	return c.host.Open(f, func(f *form.Form) error {
		to := f.Choice("access")
		if to == who.Access {
			return nil
		}
		ctx, name := c.common.Context(), c.repo.Name()
		if err := c.common.Service.RemoveCollaborator(ctx, name, who.Username); err != nil {
			return err
		}
		return c.common.Service.AddCollaborator(ctx, name, who.Username, to)
	}, c.reload("Access changed"))
}

func (c *Collaborators) remove(who ops.Collaborator) tea.Cmd {
	f := form.Confirm(c.common, fmt.Sprintf("Remove %s from %s?", who.Username, c.repo.Name()))
	return c.host.Open(f, func(*form.Form) error {
		return c.common.Service.RemoveCollaborator(c.common.Context(), c.repo.Name(), who.Username)
	}, c.reload("Removed"))
}

// Update implements tea.Model.
func (c *Collaborators) Update(msg tea.Msg) (common.Model, tea.Cmd) {
	if handled, cmd := c.host.Update(msg); handled {
		return c, cmd
	}
	switch msg := msg.(type) {
	case RepoMsg:
		c.repo = msg
		c.access, c.collabs, c.loaded, c.err, c.flash = "", nil, false, nil, ""
		c.host.Close()
		c.list = rowList{}
	case InfoMsg:
		if c.repo == nil || msg.Name != c.repo.Name() || msg.Err != nil {
			break
		}
		c.access = msg.Info.Access
		if canWrite(c.access) {
			return c, c.load()
		}
	case collabsMsg:
		if c.repo == nil || msg.name != c.repo.Name() {
			break
		}
		c.loaded, c.collabs, c.err = true, msg.collabs, msg.err
		rows := make([]string, len(c.collabs))
		for i, cl := range c.collabs {
			rows[i] = fmt.Sprintf("%-24s %s", cl.Username, cl.Access)
		}
		c.list.set(rows)
	case tea.KeyPressMsg:
		if !canWrite(c.access) || !c.loaded {
			break
		}
		if c.list.update(c.common, msg) {
			c.flash = ""
			break
		}
		var sel *ops.Collaborator
		if c.list.cursor < len(c.collabs) {
			sel = &c.collabs[c.list.cursor]
		}
		switch {
		case msg.String() == "a":
			c.flash = ""
			return c, c.add()
		case msg.String() == "x" && sel != nil:
			c.flash = ""
			return c, c.remove(*sel)
		case key.Matches(msg, c.common.KeyMap.Select) && sel != nil:
			c.flash = ""
			return c, c.change(*sel)
		}
	}
	return c, nil
}

// View implements tea.Model.
func (c *Collaborators) View() string {
	st := c.common.Styles.Form
	switch {
	case c.host.Active():
		return c.host.View()
	case c.access == "":
		return st.Hint.Render("loading…")
	case !canWrite(c.access):
		return st.Hint.Render("Seeing and changing collaborators needs write access; you have " + c.access + ".")
	case c.err != nil:
		return st.Error.Render(c.err.Error())
	case !c.loaded:
		return st.Hint.Render("loading…")
	}
	body := st.Hint.Render("No collaborators yet. Press a to add one.") + "\n"
	if len(c.collabs) > 0 {
		body = c.list.view(c.common, c.common.Height-2)
	}
	if c.flash != "" {
		body += "\n" + st.Success.Render(c.flash)
	}
	return body
}
