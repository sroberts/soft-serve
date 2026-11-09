package account

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/ui/common"
)

// fakeService is an account; the repository methods are not used here.
type fakeService struct {
	ops.Service
	anon   bool
	acct   ops.Account
	tokens []ops.AccessToken
}

func (f *fakeService) Account(context.Context) (ops.Account, error) {
	if f.anon {
		return ops.Account{}, proto.ErrUnauthorized
	}
	return f.acct, nil
}

func (f *fakeService) AccessTokens(context.Context) ([]ops.AccessToken, error) {
	return f.tokens, nil
}

func (f *fakeService) CreateAccessToken(_ context.Context, opts ops.CreateToken) (ops.NewToken, error) {
	return ops.NewToken{AccessToken: ops.AccessToken{ID: 7, Name: opts.Name}, Token: "ss_" + strings.Repeat("ab", 20)}, nil
}

func run(t *testing.T, a *Account, cmd tea.Cmd) {
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

func press(t *testing.T, a *Account, keys ...string) {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		default:
			msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		}
		_, cmd := a.Update(msg)
		run(t, a, cmd)
	}
}

func newAccount(svc ops.Service) *Account {
	c := common.NewCommon(context.Background(), 76, 30)
	c.Service = svc
	return New(c)
}

func TestViewFitsAndTokenShownOnce(t *testing.T) {
	exp := time.Now().Add(-time.Hour)
	svc := &fakeService{
		acct: ops.Account{Username: "someone", PublicKeys: []ops.PublicKey{{
			Type:        "ssh-ed25519",
			Fingerprint: "SHA256:" + strings.Repeat("x", 43),
			Key:         "ssh-ed25519 " + strings.Repeat("A", 68),
		}}},
		tokens: []ops.AccessToken{{ID: 1, Name: strings.Repeat("long name ", 6), ExpiresAt: &exp}},
	}
	a := newAccount(svc)
	run(t, a, a.Init())

	v := a.View()
	for _, l := range strings.Split(v, "\n") {
		if w := lipgloss.Width(l); w > 76 {
			t.Errorf("line is %d wide: %q", w, l)
		}
	}
	for _, want := range []string{"someone", "SHA256:", "expired", "SSH keys (1)", "Access tokens (1)"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}

	press(t, a, "t")
	for _, r := range "ci" {
		press(t, a, string(r))
	}
	press(t, a, "enter")
	tok := "ss_" + strings.Repeat("ab", 20)
	lines := strings.Split(a.View(), "\n")
	found := false
	for _, l := range lines {
		if l == tok {
			found = true
		}
	}
	if !found {
		t.Errorf("token is not on a line of its own:\n%s", a.View())
	}
	if !a.CapturingInput() {
		t.Error("the token screen should keep keys from leaving it")
	}
	press(t, a, "enter")
	if a.token != nil || strings.Contains(a.View(), tok) {
		t.Error("token still shown after it was dismissed")
	}
}

func TestAnonymous(t *testing.T) {
	a := newAccount(&fakeService{anon: true})
	run(t, a, a.Init())
	if v := a.View(); !strings.Contains(v, "No account") {
		t.Errorf("view = %q", v)
	}
	press(t, a, "a")
	if a.host.Active() {
		t.Error("an anonymous caller got a form")
	}
}

func TestTruncateMiddle(t *testing.T) {
	if got := truncateMiddle("abcdefghij", 5); got != "ab…ij" {
		t.Errorf("got %q", got)
	}
	if got := truncateMiddle("abc", 5); got != "abc" {
		t.Errorf("got %q", got)
	}
}
