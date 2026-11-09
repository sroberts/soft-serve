// Package admin is the TUI screen for server administration: the server's
// users and its settings, as the SSH `user` and `settings` commands manage
// them. Everything goes through the session's ops.Service, which allows it
// only to server admins and keeps the commands' rules, such as an admin
// not deleting their own account.
package admin

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
	"github.com/charmbracelet/soft-serve/pkg/ui/components/form"
	"github.com/charmbracelet/x/ansi"
)

var (
	openKey    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open/change"))
	newUserKey = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new user"))
	addKeyKey  = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add key"))
	removeKey  = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "remove key"))
	adminKey   = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "toggle admin"))
	renameKey  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rename"))
	deleteKey  = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete user"))
)

// accessLevels are the anon-access choices, least first.
var accessLevels = []string{"no-access", "read-only", "read-write", "admin-access"}

const fixedHint = "set in the config file: changes are saved, but it wins"

// The rows after the users in the list.
const (
	anonRow = iota
	keylessRow
	settingRows
)

// loadedMsg is the users and settings, loaded.
type loadedMsg struct {
	users    []ops.UserSummary
	settings ops.Settings
	me       string
	err      error
}

// userMsg is one user, loaded.
type userMsg struct {
	name string
	acct ops.Account
	err  error
}

// Admin is the administration screen: a list of the users and the
// settings, and a user's own page.
type Admin struct {
	common   common.Common
	users    []ops.UserSummary
	settings ops.Settings
	// me is the caller's username, so their own account is kept from
	// what they may not do to it.
	me     string
	loaded bool
	err    error
	cursor int
	offset int

	// user is the name of the user whose page is showing, or "".
	user    string
	acct    ops.Account
	uloaded bool
	uerr    error
	ucursor int
	uoffset int

	host  form.Host
	flash string
}

// New returns the administration screen.
func New(c common.Common) *Admin {
	return &Admin{common: c}
}

// SetSize implements common.Component.
func (a *Admin) SetSize(width, height int) { a.common.SetSize(width, height) }

// CapturingInput implements common.InputCapturer.
func (a *Admin) CapturingInput() bool { return a.host.Active() }

// AtRoot reports whether the list is showing, so that going back leaves
// the screen rather than a user's page.
func (a *Admin) AtRoot() bool { return a.user == "" && !a.host.Active() }

// Init loads the users and settings, and shows the list.
func (a *Admin) Init() tea.Cmd {
	a.host.Close()
	a.host.ClearErr()
	a.user, a.flash = "", ""
	return a.load()
}

func (a *Admin) load() tea.Cmd {
	svc, ctx := a.common.Service, a.common.Context()
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		users, err := svc.Users(ctx)
		if err != nil {
			return loadedMsg{err: err}
		}
		st, err := svc.Settings(ctx)
		if err != nil {
			return loadedMsg{err: err}
		}
		// An admin key need not be on an account; then nothing is theirs.
		var me string
		if acct, err := svc.Account(ctx); err == nil {
			me = acct.Username
		}
		return loadedMsg{users: users, settings: st, me: me}
	}
}

func (a *Admin) loadUser(name string) tea.Cmd {
	svc, ctx := a.common.Service, a.common.Context()
	return func() tea.Msg {
		acct, err := svc.User(ctx, name)
		return userMsg{name: name, acct: acct, err: err}
	}
}

// reload loads everything again, and the user's page if one is showing.
func (a *Admin) reload(flash string) func() tea.Cmd {
	return func() tea.Cmd {
		a.flash = flash
		cmds := []tea.Cmd{a.load()}
		if a.user != "" {
			cmds = append(cmds, a.loadUser(a.user))
		}
		return tea.Batch(cmds...)
	}
}

func (a *Admin) openUser(name string) tea.Cmd {
	a.user, a.acct, a.uloaded, a.uerr = name, ops.Account{}, false, nil
	a.ucursor, a.uoffset, a.flash = 0, 0, ""
	a.host.ClearErr()
	return a.loadUser(name)
}

func (a *Admin) items() int { return len(a.users) + settingRows }

// isSelf reports whether the user's page showing is the caller's own.
func (a *Admin) isSelf() bool {
	return a.me != "" && strings.EqualFold(a.acct.Username, a.me)
}

func (a *Admin) newUser() tea.Cmd {
	f := form.New(a.common, "New user",
		form.Text("username", "Username", "").Placeholder("letters, digits and hyphens"),
		form.Toggle("admin", "Admin", false).Hint("a server admin can do anything"),
		form.Text("key", "SSH key", "").Placeholder("ssh-ed25519 AAAA… (optional)").
			Hint("a public key they will sign in with; more can be added later"),
	)
	var name string
	return a.host.Open(f, func(f *form.Form) error {
		opts := ops.CreateUser{Username: f.String("username"), Admin: f.Bool("admin")}
		if k := f.String("key"); k != "" {
			opts.Keys = []string{k}
		}
		acct, err := a.common.Service.CreateUser(a.common.Context(), opts)
		name = acct.Username
		return err
	}, func() tea.Cmd {
		return a.reload("User " + name + " created")()
	})
}

func (a *Admin) setAnonAccess() tea.Cmd {
	field := form.Choice("level", "Anonymous access", accessLevels, a.settings.AnonAccess).
		Hint("what callers without an account get")
	if a.settings.AnonAccessFixed {
		field.Hint(fixedHint)
	}
	f := form.New(a.common, "Anonymous access", field)
	return a.host.Open(f, func(f *form.Form) error {
		level := f.Choice("level")
		_, err := a.common.Service.UpdateSettings(a.common.Context(), ops.SettingsUpdate{AnonAccess: &level})
		return err
	}, a.reload("Anonymous access changed"))
}

func (a *Admin) toggleKeyless() tea.Cmd {
	on := !a.settings.AllowKeyless
	flash := "Keyless access allowed"
	if !on {
		flash = "Keyless access refused"
	}
	a.flash = ""
	return a.host.Run(func() error {
		_, err := a.common.Service.UpdateSettings(a.common.Context(), ops.SettingsUpdate{AllowKeyless: &on})
		return err
	}, a.reload(flash))
}

func (a *Admin) addKey() tea.Cmd {
	name := a.acct.Username
	f := form.New(a.common, "Add an SSH key for "+name,
		form.Text("key", "Public key", "").Placeholder("ssh-ed25519 AAAA… them@host").
			Hint("a line from their ~/.ssh/id_ed25519.pub or similar"),
	)
	return a.host.Open(f, func(f *form.Form) error {
		_, err := a.common.Service.AddUserKey(a.common.Context(), name, f.String("key"))
		return err
	}, a.reload("Key added"))
}

func (a *Admin) removeKey(k ops.PublicKey) tea.Cmd {
	name := a.acct.Username
	f := form.Confirm(a.common, fmt.Sprintf("Remove the key %s\nfrom %s?", k.Fingerprint, name))
	return a.host.Open(f, func(*form.Form) error {
		return a.common.Service.RemoveUserKey(a.common.Context(), name, k.Fingerprint)
	}, a.reload("Key removed"))
}

func (a *Admin) toggleAdmin() tea.Cmd {
	name := a.acct.Username
	on := !a.acct.Admin
	q, flash := "Make "+name+" a server admin?", name+" is now an admin"
	if !on {
		q, flash = "Remove "+name+"'s admin rights?", name+" is no longer an admin"
	}
	f := form.Confirm(a.common, q)
	return a.host.Open(f, func(*form.Form) error {
		return a.common.Service.UpdateUser(a.common.Context(), name, ops.UserUpdate{Admin: &on})
	}, a.reload(flash))
}

func (a *Admin) rename() tea.Cmd {
	old := a.acct.Username
	hint := "collaborations and repositories follow the account"
	if a.isSelf() {
		hint = "this is your account; you will stay signed in"
	}
	f := form.New(a.common, "Rename "+old,
		form.Text("username", "Username", old).Hint(hint),
	)
	var name string
	return a.host.Open(f, func(f *form.Form) error {
		name = f.String("username")
		return a.common.Service.UpdateUser(a.common.Context(), old, ops.UserUpdate{Username: &name})
	}, func() tea.Cmd {
		a.user = strings.ToLower(name)
		return a.reload("Renamed " + old + " to " + a.user)()
	})
}

func (a *Admin) deleteUser() tea.Cmd {
	name := a.acct.Username
	f := form.Confirm(a.common, fmt.Sprintf("Delete the user %s?\nTheir keys and access tokens go with them.", name))
	return a.host.Open(f, func(*form.Form) error {
		return a.common.Service.DeleteUser(a.common.Context(), name)
	}, func() tea.Cmd {
		a.user = ""
		return a.reload("User " + name + " deleted")()
	})
}

// Update implements common.Model.
func (a *Admin) Update(msg tea.Msg) (common.Model, tea.Cmd) {
	if handled, cmd := a.host.Update(msg); handled {
		return a, cmd
	}
	switch msg := msg.(type) {
	case loadedMsg:
		a.loaded, a.err = true, msg.err
		if msg.err == nil {
			a.users, a.settings, a.me = msg.users, msg.settings, msg.me
		}
		a.cursor = max(0, min(a.cursor, a.items()-1))
	case userMsg:
		if msg.name != a.user {
			break
		}
		a.uloaded, a.uerr = true, msg.err
		if msg.err == nil {
			a.acct = msg.acct
		}
		a.ucursor = max(0, min(a.ucursor, len(a.acct.PublicKeys)-1))
	case tea.KeyPressMsg:
		if a.user != "" {
			return a, a.userKey(msg)
		}
		return a, a.listKey(msg)
	}
	return a, nil
}

func (a *Admin) listKey(msg tea.KeyPressMsg) tea.Cmd {
	if !a.loaded || a.err != nil {
		return nil
	}
	km := a.common.KeyMap
	switch {
	case key.Matches(msg, km.Up):
		a.cursor = max(0, a.cursor-1)
	case key.Matches(msg, km.Down):
		a.cursor = min(a.items()-1, a.cursor+1)
	case key.Matches(msg, km.GotoTop):
		a.cursor = 0
	case key.Matches(msg, km.GotoBottom):
		a.cursor = a.items() - 1
	case key.Matches(msg, newUserKey):
		a.flash = ""
		a.host.ClearErr()
		return a.newUser()
	case key.Matches(msg, openKey):
		a.host.ClearErr()
		if a.cursor < len(a.users) {
			return a.openUser(a.users[a.cursor].Username)
		}
		switch a.cursor - len(a.users) {
		case anonRow:
			a.flash = ""
			return a.setAnonAccess()
		case keylessRow:
			return a.toggleKeyless()
		}
	}
	return nil
}

func (a *Admin) userKey(msg tea.KeyPressMsg) tea.Cmd {
	km := a.common.KeyMap
	if key.Matches(msg, km.Back) {
		a.user, a.flash = "", ""
		return nil
	}
	if !a.uloaded || a.uerr != nil {
		return nil
	}
	n := len(a.acct.PublicKeys)
	switch {
	case key.Matches(msg, km.Up):
		a.ucursor = max(0, a.ucursor-1)
	case key.Matches(msg, km.Down):
		a.ucursor = max(0, min(n-1, a.ucursor+1))
	case key.Matches(msg, km.GotoTop):
		a.ucursor = 0
	case key.Matches(msg, km.GotoBottom):
		a.ucursor = max(0, n-1)
	case key.Matches(msg, addKeyKey):
		a.flash = ""
		return a.addKey()
	case key.Matches(msg, removeKey):
		if a.ucursor < n {
			a.flash = ""
			return a.removeKey(a.acct.PublicKeys[a.ucursor])
		}
	case key.Matches(msg, renameKey):
		a.flash = ""
		return a.rename()
	case key.Matches(msg, adminKey):
		if a.canToggleAdmin() {
			a.flash = ""
			return a.toggleAdmin()
		}
	case key.Matches(msg, deleteKey):
		if !a.isSelf() {
			a.flash = ""
			return a.deleteUser()
		}
	}
	return nil
}

// canToggleAdmin is false only for an admin's own admin flag.
func (a *Admin) canToggleAdmin() bool { return !(a.isSelf() && a.acct.Admin) }

// ShortHelp implements help.KeyMap.
func (a *Admin) ShortHelp() []key.Binding {
	km := a.common.KeyMap
	switch {
	case a.host.Active():
		return a.host.ShortHelp()
	case a.user != "":
		b := []key.Binding{km.Back}
		if !a.uloaded || a.uerr != nil {
			return b
		}
		if len(a.acct.PublicKeys) > 0 {
			b = append(b, km.UpDown, removeKey)
		}
		b = append(b, addKeyKey, renameKey)
		if a.canToggleAdmin() {
			t := adminKey
			if a.acct.Admin {
				t.SetHelp("t", "remove admin")
			} else {
				t.SetHelp("t", "make admin")
			}
			b = append(b, t)
		}
		if !a.isSelf() {
			b = append(b, deleteKey)
		}
		return b
	case !a.loaded || a.err != nil:
		return []key.Binding{km.Back}
	}
	return []key.Binding{km.Back, km.UpDown, openKey, newUserKey}
}

// FullHelp implements help.KeyMap.
func (a *Admin) FullHelp() [][]key.Binding {
	return [][]key.Binding{a.ShortHelp()}
}

// View implements common.Model.
func (a *Admin) View() string {
	st := a.common.Styles.Form
	switch {
	case a.common.Service == nil:
		return ""
	case a.host.Active():
		return a.host.View()
	case a.user != "":
		return a.userView()
	case !a.loaded:
		return st.Hint.Render("loading…")
	case a.err != nil:
		return st.Error.Render(wrap(a.err.Error(), a.common.Width))
	}

	width := a.rowWidth()
	head := []string{st.Title.Render("Server administration")}
	r := newRows(a.common)
	r.add(st.LabelFocused.Render(fmt.Sprintf("Users (%d)", len(a.users))))
	for i, u := range a.users {
		name := common.TruncateString(u.Username, max(10, width-16))
		row := fmt.Sprintf("%-*s", max(10, width-16), name)
		if u.Admin {
			row += "  " + st.Success.Render("admin")
		}
		if a.me != "" && u.Username == a.me {
			row += "  " + st.Hint.Render("you")
		}
		r.addRow(i == a.cursor, row)
	}

	r.add("", st.LabelFocused.Render("Settings"))
	s := a.settings
	anon := padLabel("Anonymous access") + st.Value.Render(s.AnonAccess)
	if s.AnonAccessFixed {
		anon += "\n" + st.Hint.Render(fixedHint)
	}
	r.addRow(a.cursor == len(a.users)+anonRow, anon)
	keyless := padLabel("Allow keyless") + st.Value.Render(yesNo(s.AllowKeyless))
	if s.AllowKeylessFixed {
		keyless += "\n" + st.Hint.Render(fixedHint)
	}
	r.addRow(a.cursor == len(a.users)+keylessRow, keyless)

	switch {
	case a.host.Busy():
		r.add("", st.Hint.Render("working…"))
	case a.host.Err() != nil:
		r.add("", st.Error.Render(wrap(a.host.Err().Error(), a.common.Width)))
	case a.flash != "":
		r.add("", st.Success.Render(a.flash))
	}
	return r.view(head, a.common.Height, &a.offset)
}

func (a *Admin) userView() string {
	st := a.common.Styles.Form
	head := []string{st.Title.Render("User " + a.user)}
	switch {
	case !a.uloaded:
		return strings.Join(append(head, st.Hint.Render("loading…")), "\n")
	case a.uerr != nil:
		return strings.Join(append(head, st.Error.Render(wrap(a.uerr.Error(), a.common.Width))), "\n")
	}
	u := a.acct
	head = append(head,
		st.Label.Render("Username  ")+st.Value.Render(u.Username),
		st.Label.Render("Admin     ")+st.Value.Render(yesNo(u.Admin)),
	)
	if a.isSelf() {
		head = append(head, st.Hint.Render(wrap("This is you: you can't delete your own account or remove "+
			"your own admin rights. Another admin can.", a.common.Width)))
	}

	width := a.rowWidth()
	r := newRows(a.common)
	r.add("", st.LabelFocused.Render(fmt.Sprintf("SSH keys (%d)", len(u.PublicKeys))))
	if len(u.PublicKeys) == 0 {
		r.add(st.Hint.Render("  No keys. Press a to add one."))
	}
	for i, k := range u.PublicKeys {
		top := common.TruncateString(k.Type+"  "+k.Fingerprint, width)
		r.addRow(i == a.ucursor, top+"\n"+truncateMiddle(keyBody(k.Key), width))
	}
	if a.flash != "" {
		r.add("", st.Success.Render(a.flash))
	}
	return r.view(head, a.common.Height, &a.uoffset)
}

func (a *Admin) rowWidth() int {
	return max(20, a.common.Width-a.common.Styles.Form.Row.GetHorizontalFrameSize())
}

// rows is a scrolling list's lines, and where the selected row is.
type rows struct {
	lines            []string
	selStart, selEnd int
	hasSel           bool
	row, selected    lipgloss.Style
}

func newRows(c common.Common) *rows {
	return &rows{row: c.Styles.Form.Row, selected: c.Styles.Form.RowSelected}
}

func (r *rows) add(lines ...string) {
	for _, l := range lines {
		r.lines = append(r.lines, strings.Split(l, "\n")...)
	}
}

func (r *rows) addRow(selected bool, row string) {
	style := r.row
	if selected {
		style = r.selected
		r.selStart, r.hasSel = len(r.lines), true
	}
	r.add(style.Render(row))
	if selected {
		r.selEnd = len(r.lines) - 1
	}
}

// view is head over as many lines as fit in height, scrolled so that the
// selected row shows.
func (r *rows) view(head []string, height int, offset *int) string {
	height = max(1, height-len(head))
	if r.hasSel {
		if r.selStart < *offset {
			*offset = r.selStart
		}
		if r.selEnd >= *offset+height {
			*offset = r.selEnd - height + 1
		}
	}
	*offset = max(0, min(*offset, len(r.lines)-height))
	end := min(len(r.lines), *offset+height)
	return strings.Join(append(head, r.lines[*offset:end]...), "\n")
}

// wrap fits s in the screen's width.
func wrap(s string, width int) string {
	return ansi.Wordwrap(s, max(20, width-2), " ,")
}

func padLabel(s string) string { return fmt.Sprintf("%-18s", s) }

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// keyBody is a key in authorized_keys form without its type.
func keyBody(k string) string {
	if _, body, ok := strings.Cut(k, " "); ok {
		return body
	}
	return k
}

// truncateMiddle fits s in width, keeping its start and end: keys of one
// type all start alike, so the end tells them apart.
func truncateMiddle(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width < 5 {
		return string(r[:width])
	}
	head := (width - 1) / 2
	tail := width - 1 - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}
