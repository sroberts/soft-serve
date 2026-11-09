package web

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// Your account: the SSH info, pubkey, token and set-username commands as
// forms on one page. Like the repository pages in ui_manage.go, every change
// goes through the caller's ops.Service and redirects back with a ?done=
// code, except making a token: its answer is the only place the token is
// ever shown.
//
//	/-/me                  your account, keys and tokens
//	/-/me/keys             add an SSH key (POST)
//	/-/me/keys/remove      remove one, by fingerprint (POST)
//	/-/me/tokens           make an access token (POST)
//	/-/me/tokens/delete    delete one, by ID (POST)
//	/-/me/username         change your username (POST)

const accountURL = "/-/me"

// tokenExpiry is a choice of how long a new access token lasts.
type tokenExpiry struct {
	Value, Label string
	// Days is how long it lasts; 0 is for ever.
	Days int
}

var tokenExpiries = []tokenExpiry{
	{"never", "Never", 0},
	{"7", "In 7 days", 7},
	{"30", "In 30 days", 30},
	{"90", "In 90 days", 90},
	{"365", "In a year", 365},
}

// defaultTokenExpiry is the choice the form starts with.
const defaultTokenExpiry = "30"

// accountData is the account page: the account, and the forms' state.
type accountData struct {
	Account
	// ViaOutpost is set when Outpost's gateway identified the caller,
	// rather than credentials of their own. Outpost finds the account by
	// name, so renaming it here would sign them out.
	ViaOutpost bool
	// SSHCommand is how to reach this server over SSH, if it serves SSH.
	SSHCommand string
	Expiries   []tokenExpiry

	KeyInput string
	KeyErr   string

	TokenName   string
	TokenExpiry string
	TokenErr    string
	// NewToken is a token just made, shown in this answer only.
	NewToken *ops.NewToken

	NewName string
	NameErr string
}

// accountPage loads the account page's data for the caller.
func (h uiHandler) accountPage(w http.ResponseWriter, r *http.Request, b *browser, p *page) (*accountData, bool) {
	acct, err := b.account(r.Context(), p.SignedIn)
	if err != nil {
		h.fail(w, r, *p, err)
		return nil, false
	}
	p.Title = "Your account"
	if acct.SignedIn {
		p.Title = acct.Username + " · Your account"
	}
	d := &accountData{
		Account:     acct,
		ViaOutpost:  p.SignedIn != "",
		SSHCommand:  sshCommand(config.FromContext(r.Context())),
		Expiries:    tokenExpiries,
		TokenExpiry: defaultTokenExpiry,
		NewName:     acct.Username,
	}
	p.Data = d
	return d, true
}

func (h uiHandler) account(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	if _, ok := h.accountPage(w, r, b, &p); ok {
		h.render(w, r, "account", p)
	}
}

// accountAction handles the account page's forms; action is the path
// after /-/me/.
func (h uiHandler) accountAction(w http.ResponseWriter, r *http.Request, b *browser, p page, action string) {
	switch action {
	case "keys", "keys/remove", "tokens", "tokens/delete", "username":
	default:
		h.fail(w, r, p, errNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, accountURL, http.StatusSeeOther)
		return
	}
	if b.user == nil {
		// The service refuses them too; this saves reading a form from
		// someone with no account to change.
		h.opsFail(w, r, b, p, proto.ErrUnauthorized)
		return
	}
	if action == "username" && p.SignedIn != "" {
		h.renderError(w, r, &p, http.StatusForbidden, "You signed in through Outpost, which finds your account by its name, so you can't rename it here. Use set-username over SSH, or soft set-username.")
		return
	}

	svc := b.service()
	ctx := r.Context()
	f, err := readForm(w, r)
	// set puts what was typed back on the form, and where its error goes.
	var set func(d *accountData, msg string)
	if err == nil {
		switch action {
		case "keys":
			key := f.Get("key")
			set = func(d *accountData, m string) { d.KeyInput, d.KeyErr = key, m }
			if _, err = svc.AddPublicKey(ctx, key); err == nil {
				http.Redirect(w, r, withDone(accountURL, "key-added"), http.StatusSeeOther)
				return
			}
		case "keys/remove":
			set = func(d *accountData, m string) { d.KeyErr = m }
			if err = svc.RemovePublicKey(ctx, f.Get("fingerprint")); err == nil {
				http.Redirect(w, r, withDone(accountURL, "key-removed"), http.StatusSeeOther)
				return
			}
		case "tokens":
			name, expiry := f.Get("name"), f.Get("expires")
			set = func(d *accountData, m string) { d.TokenName, d.TokenExpiry, d.TokenErr = name, expiry, m }
			var tok ops.NewToken
			if tok, err = createToken(r, svc, name, expiry); err == nil {
				h.showNewToken(w, r, b, p, tok)
				return
			}
		case "tokens/delete":
			set = func(d *accountData, m string) { d.TokenErr = m }
			id, perr := strconv.ParseInt(f.Get("id"), 10, 64)
			if perr != nil {
				err = proto.ErrTokenNotFound
			} else if err = svc.DeleteAccessToken(ctx, id); err == nil {
				http.Redirect(w, r, withDone(accountURL, "token-deleted"), http.StatusSeeOther)
				return
			}
		case "username":
			name := f.Get("username")
			set = func(d *accountData, m string) { d.NewName, d.NameErr = name, m }
			if err = svc.SetUsername(ctx, name); err == nil {
				http.Redirect(w, r, withDone(accountURL, "username-changed"), http.StatusSeeOther)
				return
			}
		}
	}

	// A caller who may not do this at all gets the same error page as on
	// the repository pages; anything else shows the form again.
	if status := opsStatus(err, b.user == nil); set == nil || status == http.StatusUnauthorized || status == http.StatusForbidden {
		h.opsFail(w, r, b, p, err)
		return
	}
	d, ok := h.accountPage(w, r, b, &p)
	if !ok {
		return
	}
	status, msg := h.opsMessage(r, b, err)
	// The account's own not-found errors say what wasn't found, rather
	// than the repository pages' "nothing here".
	switch {
	case errors.Is(err, proto.ErrTokenNotFound):
		msg = "That token doesn't exist, or isn't yours."
	case errors.Is(err, proto.ErrPublicKeyNotFound):
		msg = "That key isn't on your account."
	}
	set(d, msg)
	h.renderStatus(w, r, status, "account", p)
}

// createToken makes an access token that expires as the form chose.
func createToken(r *http.Request, svc ops.Service, name, expiry string) (ops.NewToken, error) {
	opts := ops.CreateToken{Name: name}
	found := false
	for _, e := range tokenExpiries {
		if e.Value == expiry {
			found = true
			if e.Days > 0 {
				at := time.Now().UTC().AddDate(0, 0, e.Days)
				opts.ExpiresAt = &at
			}
		}
	}
	if !found {
		return ops.NewToken{}, ops.Invalid("choose when the token expires")
	}
	return svc.CreateAccessToken(r.Context(), opts)
}

// showNewToken answers the post that made a token with the account page
// and the token on it. This answer is the only place it appears: it is
// not stored, so it can't go in a redirect, and nothing may keep a copy.
func (h uiHandler) showNewToken(w http.ResponseWriter, r *http.Request, b *browser, p page, tok ops.NewToken) {
	d, ok := h.accountPage(w, r, b, &p)
	if !ok {
		return
	}
	d.NewToken = &tok
	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "account", p); err != nil {
		log.FromContext(r.Context()).Error("web: render", "template", "account", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setPageHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	writeBody(w, r, http.StatusOK, buf.Bytes())
}

// sshCommand is the ssh command that reaches this server, or "" when it
// doesn't serve SSH.
func sshCommand(cfg *config.Config) string {
	if cfg == nil || !cfg.SSH.Enabled || cfg.SSH.PublicURL == "" {
		return ""
	}
	u, err := url.Parse(cfg.SSH.PublicURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	if port := u.Port(); port != "" && port != "22" {
		return "ssh -p " + port + " " + u.Hostname()
	}
	return "ssh " + u.Hostname()
}

// keyShort shortens an SSH key in authorized_keys form to its type and the
// ends of its data, which is enough to tell keys apart at a glance.
func keyShort(key string) string {
	typ, data, ok := strings.Cut(key, " ")
	if !ok || len(data) <= 32 {
		return key
	}
	return typ + " " + data[:12] + "…" + data[len(data)-12:]
}
