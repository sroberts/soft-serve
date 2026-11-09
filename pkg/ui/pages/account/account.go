// Package account is the TUI screen for the caller's own account: their
// SSH keys, access tokens and username, as the SSH `info`, `pubkey`,
// `token` and `set-username` commands manage them. Everything goes through
// the session's ops.Service, so the rules are the commands' rules.
package account

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
	"github.com/charmbracelet/soft-serve/pkg/ui/components/form"
	gossh "golang.org/x/crypto/ssh"
)

var (
	addKeyKey   = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add key"))
	newTokenKey = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "new token"))
	removeKey   = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "remove"))
	renameKey   = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rename"))
	doneKey     = key.NewBinding(key.WithKeys("enter", "esc"), key.WithHelp("enter", "done"))
)

// expiries are the choices for a new token's lifetime, in days; 0 is never.
var expiries = []struct {
	label string
	days  int
}{
	{"never", 0},
	{"7 days", 7},
	{"30 days", 30},
	{"90 days", 90},
	{"365 days", 365},
}

const dateFormat = "2006-01-02"

// loadedMsg is the account and its tokens, loaded.
type loadedMsg struct {
	acct   ops.Account
	tokens []ops.AccessToken
	err    error
}

// Account is the account screen.
type Account struct {
	common common.Common
	acct   ops.Account
	tokens []ops.AccessToken
	loaded bool
	anon   bool
	err    error
	// cursor is over the keys, then the tokens.
	cursor int
	offset int
	host   form.Host
	flash  string
	// token is a token just created, shown until dismissed and then
	// forgotten: the server keeps only its hash.
	token *ops.NewToken
}

// New returns the account screen.
func New(c common.Common) *Account {
	return &Account{common: c}
}

// SetSize implements common.Component.
func (a *Account) SetSize(width, height int) { a.common.SetSize(width, height) }

// CapturingInput implements common.InputCapturer. A new token's screen
// takes keys too, so that nothing but enter or esc leaves it.
func (a *Account) CapturingInput() bool { return a.host.Active() || a.token != nil }

// Init loads the account.
func (a *Account) Init() tea.Cmd {
	a.host.Close()
	a.token, a.flash = nil, ""
	return a.load()
}

func (a *Account) load() tea.Cmd {
	svc, ctx := a.common.Service, a.common.Context()
	if svc == nil {
		return nil
	}
	return func() tea.Msg {
		acct, err := svc.Account(ctx)
		if err != nil {
			return loadedMsg{err: err}
		}
		toks, err := svc.AccessTokens(ctx)
		return loadedMsg{acct: acct, tokens: toks, err: err}
	}
}

func (a *Account) reload(flash string) func() tea.Cmd {
	return func() tea.Cmd {
		a.flash = flash
		return a.load()
	}
}

func (a *Account) items() int { return len(a.acct.PublicKeys) + len(a.tokens) }

func (a *Account) selectedKey() *ops.PublicKey {
	if a.cursor < len(a.acct.PublicKeys) {
		return &a.acct.PublicKeys[a.cursor]
	}
	return nil
}

func (a *Account) selectedToken() *ops.AccessToken {
	i := a.cursor - len(a.acct.PublicKeys)
	if i >= 0 && i < len(a.tokens) {
		return &a.tokens[i]
	}
	return nil
}

// sessionKey is the fingerprint of the key this session is connected
// with, if any.
func (a *Account) sessionKey() string {
	if pk := a.common.PublicKey(); pk != nil {
		return gossh.FingerprintSHA256(pk)
	}
	return ""
}

func (a *Account) addKey() tea.Cmd {
	f := form.New(a.common, "Add an SSH key",
		form.Text("key", "Public key", "").Placeholder("ssh-ed25519 AAAA… you@host").
			Hint("paste a line from ~/.ssh/id_ed25519.pub or similar"),
	)
	return a.host.Open(f, func(f *form.Form) error {
		_, err := a.common.Service.AddPublicKey(a.common.Context(), f.String("key"))
		return err
	}, a.reload("Key added"))
}

func (a *Account) removeKey(k ops.PublicKey) tea.Cmd {
	q := "Remove the key " + k.Fingerprint + "?"
	if k.Fingerprint == a.sessionKey() {
		q += "\nThis session is connected with it: you will not be able to\nsign in with it again."
	}
	f := form.Confirm(a.common, q)
	return a.host.Open(f, func(*form.Form) error {
		return a.common.Service.RemovePublicKey(a.common.Context(), k.Fingerprint)
	}, a.reload("Key removed"))
}

func (a *Account) newToken() tea.Cmd {
	labels := make([]string, len(expiries))
	for i, e := range expiries {
		labels[i] = e.label
	}
	f := form.New(a.common, "New access token",
		form.Text("name", "Name", "").Placeholder("what it is for"),
		form.Choice("expires", "Expires", labels, "never"),
	)
	var tok ops.NewToken
	return a.host.Open(f, func(f *form.Form) error {
		opts := ops.CreateToken{Name: f.String("name")}
		for _, e := range expiries {
			if e.label == f.Choice("expires") && e.days > 0 {
				t := time.Now().AddDate(0, 0, e.days)
				opts.ExpiresAt = &t
			}
		}
		var err error
		tok, err = a.common.Service.CreateAccessToken(a.common.Context(), opts)
		return err
	}, func() tea.Cmd {
		a.token = &tok
		return a.load()
	})
}

func (a *Account) deleteToken(t ops.AccessToken) tea.Cmd {
	f := form.Confirm(a.common, fmt.Sprintf("Delete the token %d (%s)?", t.ID, t.Name))
	return a.host.Open(f, func(*form.Form) error {
		return a.common.Service.DeleteAccessToken(a.common.Context(), t.ID)
	}, a.reload("Token deleted"))
}

func (a *Account) rename() tea.Cmd {
	f := form.New(a.common, "Change your username",
		form.Text("username", "Username", a.acct.Username).
			Hint("collaborations and repositories follow the account"),
	)
	return a.host.Open(f, func(f *form.Form) error {
		return a.common.Service.SetUsername(a.common.Context(), f.String("username"))
	}, a.reload("Username changed"))
}

// Update implements common.Model.
func (a *Account) Update(msg tea.Msg) (common.Model, tea.Cmd) {
	if handled, cmd := a.host.Update(msg); handled {
		return a, cmd
	}
	switch msg := msg.(type) {
	case loadedMsg:
		a.loaded, a.err, a.anon = true, msg.err, errors.Is(msg.err, proto.ErrUnauthorized)
		if msg.err == nil {
			a.acct, a.tokens = msg.acct, msg.tokens
		}
		if a.cursor >= a.items() {
			a.cursor = max(0, a.items()-1)
		}
	case tea.KeyPressMsg:
		if a.token != nil {
			if key.Matches(msg, doneKey) {
				a.token.Token = ""
				a.token = nil
			}
			return a, nil
		}
		if !a.loaded || a.err != nil {
			break
		}
		km := a.common.KeyMap
		switch {
		case key.Matches(msg, km.Up):
			a.cursor = max(0, a.cursor-1)
		case key.Matches(msg, km.Down):
			a.cursor = max(0, min(a.items()-1, a.cursor+1))
		case key.Matches(msg, km.GotoTop):
			a.cursor = 0
		case key.Matches(msg, km.GotoBottom):
			a.cursor = max(0, a.items()-1)
		case key.Matches(msg, addKeyKey):
			a.flash = ""
			return a, a.addKey()
		case key.Matches(msg, newTokenKey):
			a.flash = ""
			return a, a.newToken()
		case key.Matches(msg, renameKey):
			a.flash = ""
			return a, a.rename()
		case key.Matches(msg, removeKey):
			if k := a.selectedKey(); k != nil {
				a.flash = ""
				return a, a.removeKey(*k)
			}
			if t := a.selectedToken(); t != nil {
				a.flash = ""
				return a, a.deleteToken(*t)
			}
		}
	}
	return a, nil
}

// ShortHelp implements help.KeyMap.
func (a *Account) ShortHelp() []key.Binding {
	switch {
	case a.host.Active():
		return a.host.ShortHelp()
	case a.token != nil:
		return []key.Binding{doneKey}
	case !a.loaded || a.err != nil:
		return []key.Binding{a.common.KeyMap.Back}
	}
	b := []key.Binding{a.common.KeyMap.Back}
	if a.items() > 0 {
		b = append(b, a.common.KeyMap.UpDown, removeKey)
	}
	return append(b, addKeyKey, newTokenKey, renameKey)
}

// FullHelp implements help.KeyMap.
func (a *Account) FullHelp() [][]key.Binding {
	return [][]key.Binding{a.ShortHelp()}
}

// View implements common.Model.
func (a *Account) View() string {
	st := a.common.Styles.Form
	switch {
	case a.common.Service == nil:
		return ""
	case a.host.Active():
		return a.host.View()
	case a.token != nil:
		return a.tokenView()
	case !a.loaded:
		return st.Hint.Render("loading…")
	case a.anon:
		return a.anonView()
	case a.err != nil:
		return st.Error.Render(a.err.Error())
	}

	width := max(20, a.common.Width-st.Row.GetHorizontalFrameSize())
	head := []string{st.Title.Render("Account")}
	head = append(head, strings.Split(strings.TrimRight(a.infoView(), "\n"), "\n")...)

	var lines []string
	selStart, selEnd := -1, -1
	add := func(i int, row string) {
		style := st.Row
		if i == a.cursor {
			style = st.RowSelected
			selStart = len(lines)
		}
		lines = append(lines, strings.Split(style.Render(row), "\n")...)
		if i == a.cursor {
			selEnd = len(lines) - 1
		}
	}

	lines = append(lines, "", st.LabelFocused.Render(fmt.Sprintf("SSH keys (%d)", len(a.acct.PublicKeys))))
	if len(a.acct.PublicKeys) == 0 {
		lines = append(lines, st.Hint.Render("  No keys. Press a to add one."))
	}
	session := a.sessionKey()
	for i, k := range a.acct.PublicKeys {
		top := k.Type + "  " + k.Fingerprint
		body := keyBody(k.Key)
		if k.Fingerprint == session {
			body = "this session  " + body
		}
		add(i, common.TruncateString(top, width)+"\n"+truncateMiddle(body, width))
	}

	lines = append(lines, "", st.LabelFocused.Render(fmt.Sprintf("Access tokens (%d)", len(a.tokens))))
	if len(a.tokens) == 0 {
		lines = append(lines, st.Hint.Render("  No tokens. Press t to create one."))
	}
	now := time.Now()
	for i, t := range a.tokens {
		exp := "never"
		switch {
		case t.Expired(now):
			exp = "expired"
		case t.ExpiresAt != nil:
			exp = t.ExpiresAt.Local().Format(dateFormat)
		}
		row := fmt.Sprintf("%-4d %-24s %s  %-10s", t.ID,
			common.TruncateString(t.Name, 24), t.CreatedAt.Local().Format(dateFormat), exp)
		add(len(a.acct.PublicKeys)+i, common.TruncateString(row, width))
	}
	if a.flash != "" {
		lines = append(lines, "", st.Success.Render(a.flash))
	}

	// Scroll the lists, keeping the selected row in view.
	height := max(1, a.common.Height-len(head))
	if selStart >= 0 {
		if selStart < a.offset {
			a.offset = selStart
		}
		if selEnd >= a.offset+height {
			a.offset = selEnd - height + 1
		}
	}
	a.offset = max(0, min(a.offset, len(lines)-height))
	end := min(len(lines), a.offset+height)
	return strings.Join(append(head, lines[a.offset:end]...), "\n")
}

func (a *Account) infoView() string {
	st := a.common.Styles.Form
	admin := "no"
	if a.acct.Admin {
		admin = "yes"
	}
	return st.Label.Render("Username  ") + st.Value.Render(a.acct.Username) + "\n" +
		st.Label.Render("Admin     ") + st.Value.Render(admin) + "\n"
}

// tokenView shows a new token, the one time it can be seen. The token is
// on a line of its own with no styling, so it can be selected and copied
// as it is.
func (a *Account) tokenView() string {
	st := a.common.Styles.Form
	t := a.token
	exp := "never expires"
	if t.ExpiresAt != nil {
		exp = "expires " + t.ExpiresAt.Local().Format(dateFormat)
	}
	var b strings.Builder
	b.WriteString(st.Title.Render(fmt.Sprintf("New access token %q", t.Name)) + "\n")
	b.WriteString(st.Error.Render("Copy it now: it will not be shown again.") + "\n\n")
	b.WriteString(t.Token + "\n\n")
	b.WriteString(st.Hint.Render(fmt.Sprintf("Token %d, %s. Use it as the password over HTTP.", t.ID, exp)) + "\n")
	b.WriteString(st.Hint.Render("Press enter when you have copied it."))
	return b.String()
}

// anonView explains why a caller with no account has nothing to manage.
func (a *Account) anonView() string {
	st := a.common.Styles.Form
	var b strings.Builder
	b.WriteString(st.Title.Render("No account") + "\n")
	pk := a.common.PublicKey()
	if pk == nil {
		b.WriteString("You connected without an SSH key, so you are browsing\nanonymously and there is no account to manage.\n\n")
	} else {
		b.WriteString("The key you connected with is not on any account here,\nso you are browsing anonymously and there is nothing\nto manage. Your key is:\n\n")
		b.WriteString(gossh.FingerprintSHA256(pk) + "\n\n")
	}
	b.WriteString("Keys, access tokens and usernames belong to accounts.\nAn admin can make you one with:\n\n")
	b.WriteString(st.Hint.Render("  soft user create <name> --key '<your public key>'") + "\n")
	return b.String()
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
