package admin

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
)

// fakeService is a server with an admin, "me", and one other user.
type fakeService struct {
	ops.Service
	settings ops.Settings
	users    map[string]ops.Account
	deleted  []string
}

func newFake() *fakeService {
	key := ops.PublicKey{
		Type:        "ssh-ed25519",
		Fingerprint: "SHA256:" + strings.Repeat("x", 43),
		Key:         "ssh-ed25519 " + strings.Repeat("A", 68),
	}
	return &fakeService{
		settings: ops.Settings{AnonAccess: "read-only", AllowKeyless: true, AnonAccessFixed: true},
		users: map[string]ops.Account{
			"me":    {Username: "me", Admin: true},
			"other": {Username: "other", PublicKeys: []ops.PublicKey{key}},
		},
	}
}

func (f *fakeService) Account(context.Context) (ops.Account, error) { return f.users["me"], nil }

func (f *fakeService) Users(context.Context) ([]ops.UserSummary, error) {
	return []ops.UserSummary{{Username: "me", Admin: true}, {Username: "other"}}, nil
}

func (f *fakeService) User(_ context.Context, name string) (ops.Account, error) {
	if u, ok := f.users[name]; ok {
		return u, nil
	}
	return ops.Account{}, proto.ErrUserNotFound
}

func (f *fakeService) Settings(context.Context) (ops.Settings, error) { return f.settings, nil }

func (f *fakeService) DeleteUser(_ context.Context, name string) error {
	if name == "other" {
		names := make([]string, 12)
		for i := range names {
			names[i] = fmt.Sprintf("group/repository-%d", i)
		}
		return fmt.Errorf("%w: %s", proto.ErrUserOwnsRepos, strings.Join(names, ", "))
	}
	f.deleted = append(f.deleted, name)
	return nil
}

func run(t *testing.T, a *Admin, cmd tea.Cmd) {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				run(t, a, c)
			}
			return
		}
		_, cmd = a.Update(msg)
	}
}

func press(t *testing.T, a *Admin, keys ...string) {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		default:
			msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		}
		_, cmd := a.Update(msg)
		run(t, a, cmd)
	}
}

func newAdmin(svc ops.Service) *Admin {
	c := common.NewCommon(context.Background(), 76, 30)
	c.Service = svc
	return New(c)
}

func fits(t *testing.T, v string) {
	t.Helper()
	for _, l := range strings.Split(v, "\n") {
		if w := lipgloss.Width(l); w > 76 {
			t.Errorf("line is %d wide: %q", w, l)
		}
	}
}

func TestList(t *testing.T) {
	a := newAdmin(newFake())
	run(t, a, a.Init())
	v := a.View()
	fits(t, v)
	for _, want := range []string{"Users (2)", "other", "read-only", "config file"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if !a.AtRoot() {
		t.Error("the list is the screen's root")
	}
}

func TestOwnAccount(t *testing.T) {
	a := newAdmin(newFake())
	run(t, a, a.Init())
	press(t, a, "enter") // me
	v := a.View()
	fits(t, v)
	if !strings.Contains(v, "This is you") {
		t.Errorf("no reason given:\n%s", v)
	}
	for _, k := range []string{"d", "t"} {
		press(t, a, k)
		if a.host.Active() {
			t.Errorf("%s opened a form on the admin's own account", k)
		}
	}
	for _, b := range a.ShortHelp() {
		if h := b.Help().Key; h == "d" || h == "t" {
			t.Errorf("%s offered on the admin's own account", h)
		}
	}
	press(t, a, "esc")
	if !a.AtRoot() {
		t.Error("esc did not go back to the list")
	}
}

func TestDeleteOwnerError(t *testing.T) {
	svc := newFake()
	a := newAdmin(svc)
	run(t, a, a.Init())
	press(t, a, "j", "enter")
	if v := a.View(); !strings.Contains(v, "User other") || !strings.Contains(v, "SSH keys (1)") {
		t.Fatalf("user page:\n%s", v)
	}
	press(t, a, "d")
	if v := a.View(); !strings.Contains(v, "Delete the user other?") {
		t.Fatalf("no confirmation naming the user:\n%s", v)
	}
	press(t, a, "y")
	v := a.View()
	fits(t, v)
	if !strings.Contains(v, "group/repository-11") || !strings.Contains(v, "owns repositories") {
		t.Errorf("error not shown in full:\n%s", v)
	}
	if len(svc.deleted) != 0 {
		t.Error("deleted anyway")
	}
}
