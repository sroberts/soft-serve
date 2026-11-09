package ops

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/utils"
)

// UserSummary is a user in a list.
type UserSummary struct {
	Username string `json:"username"`
	Admin    bool   `json:"admin"`
}

// CreateUser describes a user to create.
type CreateUser struct {
	Username string `json:"username"`
	Admin    bool   `json:"admin,omitempty"`
	// Keys are SSH keys in authorized_keys form.
	Keys []string `json:"keys,omitempty"`
}

// UserUpdate is the changes to make to a user; nil fields stay as they are.
type UserUpdate struct {
	Username *string `json:"username,omitempty"`
	Admin    *bool   `json:"admin,omitempty"`
}

// Settings are the server-wide settings.
type Settings struct {
	// AnonAccess is what anonymous callers get: no-access, read-only,
	// read-write or admin-access.
	AnonAccess string `json:"anon_access"`
	// AllowKeyless is whether callers without an SSH key, such as over
	// HTTP, may read public repositories.
	AllowKeyless bool `json:"allow_keyless"`
	// AnonAccessFixed and AllowKeylessFixed are set when the server's
	// config file sets the value. Changes are still saved, but the config
	// wins until it no longer sets it.
	AnonAccessFixed   bool `json:"anon_access_fixed"`
	AllowKeylessFixed bool `json:"allow_keyless_fixed"`
}

// SettingsUpdate is the settings to change; nil fields stay as they are.
type SettingsUpdate struct {
	AnonAccess   *string `json:"anon_access,omitempty"`
	AllowKeyless *bool   `json:"allow_keyless,omitempty"`
}

// admin is the gate for server administration.
func (l *Local) admin() error {
	if !l.actor.Admin {
		return proto.ErrUnauthorized
	}
	return nil
}

// isSelf reports whether u is the actor's own account.
func (l *Local) isSelf(u proto.User) bool {
	return l.actor.User != nil && l.actor.User.ID() == u.ID()
}

// user finds a user by name for an admin.
func (l *Local) user(ctx context.Context, name string) (proto.User, error) {
	if err := l.admin(); err != nil {
		return nil, err
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if utils.ValidateUsername(name) != nil {
		return nil, proto.ErrUserNotFound
	}
	return l.be.User(ctx, name)
}

// Users implements Service.
func (l *Local) Users(ctx context.Context) ([]UserSummary, error) {
	if err := l.admin(); err != nil {
		return nil, err
	}
	names, err := l.be.Users(ctx)
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	out := make([]UserSummary, 0, len(names))
	for _, n := range names {
		u, err := l.be.User(ctx, n)
		if err != nil {
			return nil, err
		}
		out = append(out, UserSummary{Username: u.Username(), Admin: u.IsAdmin()})
	}
	return out, nil
}

// User implements Service.
func (l *Local) User(ctx context.Context, name string) (Account, error) {
	u, err := l.user(ctx, name)
	if err != nil {
		return Account{}, err
	}
	return accountOf(u), nil
}

// CreateUser implements Service.
func (l *Local) CreateUser(ctx context.Context, opts CreateUser) (Account, error) {
	if err := l.admin(); err != nil {
		return Account{}, err
	}
	name := strings.ToLower(strings.TrimSpace(utils.Sanitize(opts.Username)))
	if err := utils.ValidateUsername(name); err != nil {
		return Account{}, Invalid("%s", err.Error())
	}
	uo := proto.UserOptions{Admin: opts.Admin}
	for _, k := range opts.Keys {
		pk, err := parseKey(k)
		if err != nil {
			return Account{}, err
		}
		uo.PublicKeys = append(uo.PublicKeys, pk)
	}
	u, err := l.be.CreateUser(ctx, name, uo)
	if err != nil {
		if errors.Is(err, db.ErrDuplicateKey) {
			// The name, or one of the keys, is taken.
			if _, e := l.be.User(ctx, name); e == nil {
				return Account{}, proto.ErrUserExist
			}
			return Account{}, proto.ErrPublicKeyInUse
		}
		return Account{}, err
	}
	return accountOf(u), nil
}

// DeleteUser implements Service. Admins can't delete their own account, so
// a server can't be left with no admin by accident.
func (l *Local) DeleteUser(ctx context.Context, name string) error {
	u, err := l.user(ctx, name)
	if err != nil {
		return err
	}
	if l.isSelf(u) {
		return Invalid("you can't delete your own account; another admin can")
	}
	return l.be.DeleteUser(ctx, u.Username())
}

// UpdateUser implements Service. Admins can't take away their own admin
// rights, for the same reason they can't delete themselves.
func (l *Local) UpdateUser(ctx context.Context, name string, up UserUpdate) error {
	u, err := l.user(ctx, name)
	if err != nil {
		return err
	}
	if up.Admin != nil && *up.Admin != u.IsAdmin() {
		if !*up.Admin && l.isSelf(u) {
			return Invalid("you can't remove your own admin rights; another admin can")
		}
		if err := l.be.SetAdmin(ctx, u.Username(), *up.Admin); err != nil {
			return err
		}
	}
	if up.Username != nil {
		return l.rename(ctx, u, *up.Username)
	}
	return nil
}

// AddUserKey implements Service.
func (l *Local) AddUserKey(ctx context.Context, name, key string) (PublicKey, error) {
	u, err := l.user(ctx, name)
	if err != nil {
		return PublicKey{}, err
	}
	return l.addKey(ctx, u, key)
}

// RemoveUserKey implements Service.
func (l *Local) RemoveUserKey(ctx context.Context, name, key string) error {
	u, err := l.user(ctx, name)
	if err != nil {
		return err
	}
	return l.removeKey(ctx, u, key)
}

// Settings implements Service.
func (l *Local) Settings(ctx context.Context) (Settings, error) {
	if err := l.admin(); err != nil {
		return Settings{}, err
	}
	cfg := config.FromContext(ctx)
	return Settings{
		AnonAccess:        l.be.AnonAccess(ctx).String(),
		AllowKeyless:      l.be.AllowKeyless(ctx),
		AnonAccessFixed:   cfg != nil && cfg.AnonAccess != nil,
		AllowKeylessFixed: cfg != nil && cfg.AllowKeyless != nil,
	}, nil
}

// UpdateSettings implements Service.
func (l *Local) UpdateSettings(ctx context.Context, up SettingsUpdate) (Settings, error) {
	if err := l.admin(); err != nil {
		return Settings{}, err
	}
	if up.AnonAccess != nil {
		al := access.ParseAccessLevel(*up.AnonAccess)
		if al < 0 {
			return Settings{}, Invalid("invalid access level: %s. Please choose one of the following: %s", *up.AnonAccess, accessLevels)
		}
		if err := l.be.SetAnonAccess(ctx, al); err != nil {
			return Settings{}, err
		}
	}
	if up.AllowKeyless != nil {
		if err := l.be.SetAllowKeyless(ctx, *up.AllowKeyless); err != nil {
			return Settings{}, err
		}
	}
	return l.Settings(ctx)
}

var accessLevels = []string{
	access.NoAccess.String(), access.ReadOnlyAccess.String(),
	access.ReadWriteAccess.String(), access.AdminAccess.String(),
}
