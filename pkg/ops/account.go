package ops

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/charmbracelet/soft-serve/pkg/sshutils"
	"github.com/charmbracelet/soft-serve/pkg/utils"
	gossh "golang.org/x/crypto/ssh"
)

// maxTokenName bounds an access token's name.
const maxTokenName = 100

// Account is the actor's own account, as `info` shows it.
type Account struct {
	Username   string      `json:"username"`
	Admin      bool        `json:"admin"`
	PublicKeys []PublicKey `json:"keys"`
}

// PublicKey is one of an account's SSH keys.
type PublicKey struct {
	// Key is the key in authorized_keys form, without a comment.
	Key         string `json:"key"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

// AccessToken is one of an account's access tokens, without the token.
type AccessToken struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Expired reports whether the token has expired by now.
func (t AccessToken) Expired(now time.Time) bool {
	return t.ExpiresAt != nil && now.After(*t.ExpiresAt)
}

// CreateToken describes an access token to create.
type CreateToken struct {
	Name string `json:"name"`
	// ExpiresAt is when the token stops working; nil means never.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// NewToken is a token just created. Token is shown this once; the server
// keeps only its hash.
type NewToken struct {
	AccessToken
	Token string `json:"token"`
}

// self is the actor's account as it is now, not as it was when the session
// or request began: keys and names change.
func (l *Local) self(ctx context.Context) (proto.User, error) {
	if l.actor.User == nil {
		return nil, proto.ErrUnauthorized
	}
	u, err := l.be.UserByID(ctx, l.actor.User.ID())
	if errors.Is(err, proto.ErrUserNotFound) {
		return nil, proto.ErrUnauthorized
	}
	return u, err
}

// PublicKeyOf describes k.
func PublicKeyOf(k gossh.PublicKey) PublicKey {
	return PublicKey{
		Key:         sshutils.MarshalAuthorizedKey(k),
		Type:        k.Type(),
		Fingerprint: gossh.FingerprintSHA256(k),
	}
}

// Account implements Service.
func (l *Local) Account(ctx context.Context) (Account, error) {
	u, err := l.self(ctx)
	if err != nil {
		return Account{}, err
	}
	out := Account{Username: u.Username(), Admin: u.IsAdmin(), PublicKeys: []PublicKey{}}
	for _, k := range u.PublicKeys() {
		out.PublicKeys = append(out.PublicKeys, PublicKeyOf(k))
	}
	return out, nil
}

// AddPublicKey implements Service.
func (l *Local) AddPublicKey(ctx context.Context, key string) (PublicKey, error) {
	u, err := l.self(ctx)
	if err != nil {
		return PublicKey{}, err
	}
	pk, _, err := sshutils.ParseAuthorizedKey(strings.TrimSpace(key))
	if err != nil {
		return PublicKey{}, Invalid("not an SSH public key; paste a line like the one in ~/.ssh/id_ed25519.pub")
	}
	if slices.ContainsFunc(u.PublicKeys(), func(k gossh.PublicKey) bool { return sameKey(k, pk) }) {
		return PublicKey{}, proto.ErrPublicKeyExist
	}
	if err := l.be.AddPublicKey(ctx, u.Username(), pk); err != nil {
		if errors.Is(err, db.ErrDuplicateKey) {
			return PublicKey{}, proto.ErrPublicKeyInUse
		}
		return PublicKey{}, err
	}
	return PublicKeyOf(pk), nil
}

// RemovePublicKey implements Service.
func (l *Local) RemovePublicKey(ctx context.Context, key string) error {
	u, err := l.self(ctx)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	var match func(gossh.PublicKey) bool
	if strings.HasPrefix(key, "SHA256:") {
		match = func(k gossh.PublicKey) bool { return gossh.FingerprintSHA256(k) == key }
	} else {
		pk, _, err := sshutils.ParseAuthorizedKey(key)
		if err != nil {
			return Invalid("give the key as an SSH public key or its SHA256 fingerprint")
		}
		match = func(k gossh.PublicKey) bool { return sameKey(k, pk) }
	}
	i := slices.IndexFunc(u.PublicKeys(), match)
	if i < 0 {
		return proto.ErrPublicKeyNotFound
	}
	return l.be.RemovePublicKey(ctx, u.Username(), u.PublicKeys()[i])
}

func sameKey(a, b gossh.PublicKey) bool {
	return a.Type() == b.Type() && string(a.Marshal()) == string(b.Marshal())
}

// AccessTokens implements Service.
func (l *Local) AccessTokens(ctx context.Context) ([]AccessToken, error) {
	u, err := l.self(ctx)
	if err != nil {
		return nil, err
	}
	toks, err := l.be.ListAccessTokens(ctx, u)
	if err != nil {
		return nil, err
	}
	out := make([]AccessToken, 0, len(toks))
	for _, t := range toks {
		out = append(out, tokenOut(t))
	}
	slices.SortFunc(out, func(a, b AccessToken) int { return int(a.ID - b.ID) })
	return out, nil
}

func tokenOut(t proto.AccessToken) AccessToken {
	at := AccessToken{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt.UTC()}
	if !t.ExpiresAt.IsZero() {
		e := t.ExpiresAt.UTC()
		at.ExpiresAt = &e
	}
	return at
}

// CreateAccessToken implements Service.
func (l *Local) CreateAccessToken(ctx context.Context, opts CreateToken) (NewToken, error) {
	u, err := l.self(ctx)
	if err != nil {
		return NewToken{}, err
	}
	name := strings.TrimSpace(utils.Sanitize(opts.Name))
	if name == "" {
		return NewToken{}, Invalid("give the token a name")
	}
	if utf8.RuneCountInString(name) > maxTokenName {
		return NewToken{}, Invalid("token names are at most %d characters", maxTokenName)
	}
	var expires time.Time
	if opts.ExpiresAt != nil {
		expires = *opts.ExpiresAt
	}
	before, err := l.be.ListAccessTokens(ctx, u)
	if err != nil {
		return NewToken{}, err
	}
	tok, err := l.be.CreateAccessToken(ctx, u, name, expires)
	if err != nil {
		return NewToken{}, err
	}
	out := NewToken{Token: tok, AccessToken: AccessToken{Name: name, CreatedAt: time.Now().UTC()}}
	if !expires.IsZero() {
		e := expires.UTC()
		out.ExpiresAt = &e
	}
	// The backend doesn't return the new row; it is the one not there before.
	after, err := l.be.ListAccessTokens(ctx, u)
	if err == nil {
		for _, t := range after {
			if !slices.ContainsFunc(before, func(b proto.AccessToken) bool { return b.ID == t.ID }) {
				out.AccessToken = tokenOut(t)
			}
		}
	}
	return out, nil
}

// DeleteAccessToken implements Service. A token of someone else's is
// reported as not found, the same as one that doesn't exist.
func (l *Local) DeleteAccessToken(ctx context.Context, id int64) error {
	u, err := l.self(ctx)
	if err != nil {
		return err
	}
	toks, err := l.be.ListAccessTokens(ctx, u)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(toks, func(t proto.AccessToken) bool { return t.ID == id }) {
		return proto.ErrTokenNotFound
	}
	return l.be.DeleteAccessToken(ctx, u, id)
}

// SetUsername implements Service.
func (l *Local) SetUsername(ctx context.Context, username string) error {
	u, err := l.self(ctx)
	if err != nil {
		return err
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if err := utils.ValidateUsername(username); err != nil {
		return Invalid("%s", err.Error())
	}
	if username == u.Username() {
		return nil
	}
	if err := l.be.SetUsername(ctx, u.Username(), username); err != nil {
		if errors.Is(err, db.ErrDuplicateKey) {
			return proto.ErrUserExist
		}
		return err
	}
	return nil
}
