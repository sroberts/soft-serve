package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// Server administration: the SSH user and settings commands as forms, for
// server admins. Like the repository pages in ui_manage.go, every change goes
// through the caller's ops.Service, which refuses anyone but an admin, and
// redirects back with a ?done= code. The pages refuse non-admins before
// looking anything up, so whether a user exists is never told apart from
// being refused.
//
//	/-/admin                               the users
//	/-/admin/users                         list users, and create one (POST)
//	/-/admin/users/{name}                  one user
//	/-/admin/users/{name}/keys             add an SSH key (POST)
//	/-/admin/users/{name}/keys/remove      remove one, by fingerprint (POST)
//	/-/admin/users/{name}/admin            make or unmake an admin (POST)
//	/-/admin/users/{name}/rename           rename (POST)
//	/-/admin/users/{name}/delete           delete, after confirming
//	/-/admin/settings                      anon-access and allow-keyless

const (
	adminUsersURL    = "/-/admin/users"
	adminSettingsURL = "/-/admin/settings"
)

// adminUserURL is a user's page, or one of its actions.
func adminUserURL(name, action string) string {
	u := adminUsersURL + "/" + url.PathEscape(name)
	if action != "" {
		u += "/" + action
	}
	return u
}

// anonLevel is an anon-access level as the settings form offers it.
type anonLevel struct{ Value, Label string }

var anonLevels = []anonLevel{
	{access.NoAccess.String(), "No access"},
	{access.ReadOnlyAccess.String(), "Read-only"},
	{access.ReadWriteAccess.String(), "Read-write"},
	{access.AdminAccess.String(), "Admin access"},
}

// admin serves /-/admin and everything under it; rest is the path after
// /-/admin, without its slash.
func (h uiHandler) admin(w http.ResponseWriter, r *http.Request, b *browser, p page, rest string) {
	// Refuse before anything is looked up or read, so a non-admin learns
	// nothing from the answer, not even which users exist.
	if b.user == nil || !b.user.IsAdmin() {
		h.opsFail(w, r, b, p, proto.ErrUnauthorized)
		return
	}
	switch {
	case rest == "":
		http.Redirect(w, r, adminUsersURL, http.StatusSeeOther)
	case rest == "users":
		h.adminUsers(w, r, b, p)
	case rest == "settings":
		h.adminSettings(w, r, b, p)
	case strings.HasPrefix(rest, "users/"):
		name, action, _ := strings.Cut(strings.TrimPrefix(rest, "users/"), "/")
		h.adminUser(w, r, b, p, name, action)
	default:
		h.fail(w, r, p, errNotFound)
	}
}

// --- Users ---

type adminUsersData struct {
	Users []ops.UserSummary
	// Self is the caller's username.
	Self string

	Name, Key string
	Admin     bool
	Err       string
}

func (h uiHandler) adminUsers(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "users"
	p.Title = "Users"
	svc := b.service()
	d := &adminUsersData{Self: b.user.Username()}
	p.Data = d

	var err error
	if r.Method == http.MethodPost {
		var f url.Values
		if f, err = readForm(w, r); err == nil {
			d.Name, d.Key, d.Admin = strings.TrimSpace(f.Get("username")), f.Get("key"), checked(f, "admin")
			opts := ops.CreateUser{Username: d.Name, Admin: d.Admin}
			if strings.TrimSpace(d.Key) != "" {
				opts.Keys = []string{d.Key}
			}
			var acct ops.Account
			if acct, err = svc.CreateUser(r.Context(), opts); err == nil {
				http.Redirect(w, r, withDone(adminUserURL(acct.Username, ""), "user-created"), http.StatusSeeOther)
				return
			}
		}
	}

	users, uerr := svc.Users(r.Context())
	if uerr != nil {
		h.opsFail(w, r, b, p, uerr)
		return
	}
	d.Users = users
	if err != nil {
		h.renderForm(w, r, b, p, "admin-users", err, func(m string) { d.Err = m })
		return
	}
	h.render(w, r, "admin-users", p)
}

type adminUserData struct {
	ops.Account
	// Self is set on the caller's own page.
	Self bool
	// BehindOutpost is set when Outpost's gateway fronts the server, so a
	// web sign-in finds its account by name.
	BehindOutpost bool
	// ViaOutpost is set when the caller signed in through Outpost.
	ViaOutpost bool

	KeyInput, KeyErr string
	AdminErr         string
	NewName, NameErr string
	DeleteErr        string
	// OwnedRepos are the repositories that stopped a delete.
	OwnedRepos []string
}

// adminUserPage loads a user's page for the caller.
func (h uiHandler) adminUserPage(w http.ResponseWriter, r *http.Request, b *browser, p *page, name string) (*adminUserData, bool) {
	acct, err := b.service().User(r.Context(), name)
	if err != nil {
		h.opsFail(w, r, b, *p, err)
		return nil, false
	}
	p.Tab = "users"
	p.Title = acct.Username + " · Users"
	d := &adminUserData{
		Account:       acct,
		Self:          acct.Username == b.user.Username(),
		BehindOutpost: h.verifier != nil,
		ViaOutpost:    p.SignedIn != "",
		NewName:       acct.Username,
	}
	p.Data = d
	return d, true
}

func (h uiHandler) adminUser(w http.ResponseWriter, r *http.Request, b *browser, p page, name, action string) {
	switch action {
	case "":
		if r.Method == http.MethodPost {
			h.fail(w, r, p, errNotFound)
			return
		}
		if _, ok := h.adminUserPage(w, r, b, &p, name); ok {
			h.render(w, r, "admin-user", p)
		}
		return
	case "delete":
		h.adminDeleteUser(w, r, b, p, name)
		return
	case "keys", "keys/remove", "admin", "rename":
	default:
		h.fail(w, r, p, errNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, adminUserURL(name, ""), http.StatusSeeOther)
		return
	}

	svc := b.service()
	ctx := r.Context()
	f, err := readForm(w, r)
	var set func(d *adminUserData, msg string)
	back := adminUserURL(name, "")
	if err == nil {
		switch action {
		case "keys":
			key := f.Get("key")
			set = func(d *adminUserData, m string) { d.KeyInput, d.KeyErr = key, m }
			if _, err = svc.AddUserKey(ctx, name, key); err == nil {
				http.Redirect(w, r, withDone(back, "key-added"), http.StatusSeeOther)
				return
			}
		case "keys/remove":
			set = func(d *adminUserData, m string) { d.KeyErr = m }
			if err = svc.RemoveUserKey(ctx, name, f.Get("fingerprint")); err == nil {
				http.Redirect(w, r, withDone(back, "key-removed"), http.StatusSeeOther)
				return
			}
		case "admin":
			set = func(d *adminUserData, m string) { d.AdminErr = m }
			var admin bool
			switch f.Get("admin") {
			case "true":
				admin = true
			case "false":
			default:
				err = ops.Invalid("choose whether they're an admin")
			}
			if err == nil {
				if err = svc.UpdateUser(ctx, name, ops.UserUpdate{Admin: &admin}); err == nil {
					done := "admin-revoked"
					if admin {
						done = "admin-granted"
					}
					http.Redirect(w, r, withDone(back, done), http.StatusSeeOther)
					return
				}
			}
		case "rename":
			newName := strings.TrimSpace(f.Get("username"))
			set = func(d *adminUserData, m string) { d.NewName, d.NameErr = newName, m }
			if p.SignedIn != "" && strings.EqualFold(strings.TrimSpace(name), b.user.Username()) {
				// As on the account page: Outpost finds the caller's
				// account by name, so this would sign them out.
				h.renderError(w, r, &p, http.StatusForbidden, "You signed in through Outpost, which finds your account by its name, so you can't rename it here. Use set-username over SSH, or soft set-username.")
				return
			}
			if err = svc.UpdateUser(ctx, name, ops.UserUpdate{Username: &newName}); err == nil {
				target := adminUsersURL
				if acct, aerr := svc.User(ctx, newName); aerr == nil {
					target = adminUserURL(acct.Username, "")
				}
				http.Redirect(w, r, withDone(target, "user-renamed"), http.StatusSeeOther)
				return
			}
		}
	}
	h.adminUserFailed(w, r, b, p, name, err, set)
}

// adminUserFailed shows a user's page again with why a change failed, or
// an error page when there is no page to show.
func (h uiHandler) adminUserFailed(w http.ResponseWriter, r *http.Request, b *browser, p page, name string, err error, set func(*adminUserData, string)) {
	if status := opsStatus(err, false); set == nil || status == http.StatusUnauthorized || status == http.StatusForbidden || errors.Is(err, proto.ErrUserNotFound) {
		h.opsFail(w, r, b, p, err)
		return
	}
	d, ok := h.adminUserPage(w, r, b, &p, name)
	if !ok {
		return
	}
	status, msg := h.opsMessage(r, b, err)
	switch {
	case errors.Is(err, proto.ErrPublicKeyNotFound):
		msg = "That key isn't on this account."
	case errors.Is(err, proto.ErrPublicKeyExist):
		msg = "They already have this key."
	}
	set(d, msg)
	h.renderStatus(w, r, status, "admin-user", p)
}

func (h uiHandler) adminDeleteUser(w http.ResponseWriter, r *http.Request, b *browser, p page, name string) {
	if r.Method != http.MethodPost {
		d, ok := h.adminUserPage(w, r, b, &p, name)
		if !ok {
			return
		}
		if d.Self {
			h.opsFail(w, r, b, p, ops.Invalid("you can't delete your own account; another admin can"))
			return
		}
		p.Title = "Delete " + d.Username
		p.Data = confirmData{
			Heading: "Delete this user?",
			Message: "Their SSH keys, access tokens and collaborator access go with them. This can't be undone.",
			What:    d.Username,
			Action:  adminUserURL(d.Username, "delete"),
			Button:  "Delete user",
			Cancel:  adminUserURL(d.Username, ""),
		}
		h.render(w, r, "confirm", p)
		return
	}
	err := b.service().DeleteUser(r.Context(), name)
	if err == nil {
		http.Redirect(w, r, withDone(adminUsersURL, "user-deleted"), http.StatusSeeOther)
		return
	}
	if !errors.Is(err, proto.ErrUserOwnsRepos) {
		h.adminUserFailed(w, r, b, p, name, err, func(d *adminUserData, m string) { d.DeleteErr = m })
		return
	}
	d, ok := h.adminUserPage(w, r, b, &p, name)
	if !ok {
		return
	}
	d.DeleteErr = "They still own these repositories. Delete them first."
	d.OwnedRepos = ownedRepos(err)
	h.renderStatus(w, r, http.StatusConflict, "admin-user", p)
}

// ownedRepos are the repository names an ErrUserOwnsRepos carries after
// its own message.
func ownedRepos(err error) []string {
	_, names, ok := strings.Cut(err.Error(), proto.ErrUserOwnsRepos.Error()+": ")
	if !ok || names == "" {
		return nil
	}
	return strings.Split(names, ", ")
}

// --- Settings ---

type adminSettingsData struct {
	ops.Settings
	Levels []anonLevel
	Err    string
}

func (h uiHandler) adminSettings(w http.ResponseWriter, r *http.Request, b *browser, p page) {
	p.Tab = "settings"
	p.Title = "Server settings"
	svc := b.service()
	s, err := svc.Settings(r.Context())
	if err != nil {
		h.opsFail(w, r, b, p, err)
		return
	}
	d := &adminSettingsData{Settings: s, Levels: anonLevels}
	p.Data = d
	if r.Method != http.MethodPost {
		h.render(w, r, "admin-settings", p)
		return
	}

	f, err := readForm(w, r)
	if err == nil {
		// Only what changed is sent, so a value the config file fixes
		// isn't saved over just because the form showed it.
		var up ops.SettingsUpdate
		if v := f.Get("anon_access"); v != s.AnonAccess {
			up.AnonAccess = &v
		}
		if v := checked(f, "allow_keyless"); v != s.AllowKeyless {
			up.AllowKeyless = &v
		}
		if up.AnonAccess != nil && access.ParseAccessLevel(*up.AnonAccess) < 0 {
			// The service would list the levels; the form offers them.
			err = ops.Invalid("choose one of the access levels offered")
		} else if _, err = svc.UpdateSettings(r.Context(), up); err == nil {
			http.Redirect(w, r, withDone(adminSettingsURL, "saved"), http.StatusSeeOther)
			return
		}
	}
	if now, serr := svc.Settings(r.Context()); serr == nil {
		d.Settings = now
	}
	h.renderForm(w, r, b, p, "admin-settings", err, func(m string) { d.Err = m })
}
