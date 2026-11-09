package ops

import (
	"testing"
	"time"

	"github.com/charmbracelet/soft-serve/pkg/proto"
)

const (
	keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOYGvLBJnHJYF4P5mO4w0Uc4H0YFqL2bF3zQ5mJ0gV0m a@example"
	keyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ6t3V0sPqPpFq7sJ2nL5u3o2wM8Kc1yYf3yC0Hn9m8D"
)

func TestAccountNeedsSignIn(t *testing.T) {
	f := newFixture(t)
	anon := f.as(nil)
	_, err := anon.Account(f.ctx)
	wantErr(t, err, proto.ErrUnauthorized)
	_, err = anon.AddPublicKey(f.ctx, keyA)
	wantErr(t, err, proto.ErrUnauthorized)
	_, err = anon.CreateAccessToken(f.ctx, CreateToken{Name: "x"})
	wantErr(t, err, proto.ErrUnauthorized)
	wantErr(t, anon.SetUsername(f.ctx, "someone"), proto.ErrUnauthorized)
}

func TestPublicKeys(t *testing.T) {
	f := newFixture(t)
	r, w := f.as(f.reader), f.as(f.writer)

	k, err := r.AddPublicKey(f.ctx, keyA)
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "ssh-ed25519" || k.Fingerprint == "" {
		t.Errorf("added %+v", k)
	}
	_, err = r.AddPublicKey(f.ctx, keyA)
	wantErr(t, err, proto.ErrPublicKeyExist)
	_, err = w.AddPublicKey(f.ctx, keyA)
	wantErr(t, err, proto.ErrPublicKeyInUse)
	if _, err := r.AddPublicKey(f.ctx, "not a key"); !IsInvalid(err) {
		t.Errorf("bad key: err = %v", err)
	}

	// The account is read fresh, so the new key shows at once.
	acct, err := r.Account(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(acct.PublicKeys) != 1 || acct.Username != "reader" {
		t.Fatalf("account = %+v", acct)
	}

	// Someone else can't remove it, by key or by fingerprint.
	wantErr(t, w.RemovePublicKey(f.ctx, keyA), proto.ErrPublicKeyNotFound)
	wantErr(t, w.RemovePublicKey(f.ctx, k.Fingerprint), proto.ErrPublicKeyNotFound)

	if _, err := r.AddPublicKey(f.ctx, keyB); err != nil {
		t.Fatal(err)
	}
	if err := r.RemovePublicKey(f.ctx, k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := r.RemovePublicKey(f.ctx, keyB); err != nil {
		t.Fatal(err)
	}
	wantErr(t, r.RemovePublicKey(f.ctx, keyB), proto.ErrPublicKeyNotFound)
	acct, _ = r.Account(f.ctx)
	if len(acct.PublicKeys) != 0 {
		t.Errorf("keys left: %+v", acct.PublicKeys)
	}
}

func TestAccessTokens(t *testing.T) {
	f := newFixture(t)
	r, w := f.as(f.reader), f.as(f.writer)

	if _, err := r.CreateAccessToken(f.ctx, CreateToken{Name: "  "}); !IsInvalid(err) {
		t.Errorf("blank name: err = %v", err)
	}
	exp := time.Now().Add(time.Hour)
	tok, err := r.CreateAccessToken(f.ctx, CreateToken{Name: "ci", ExpiresAt: &exp})
	if err != nil {
		t.Fatal(err)
	}
	if tok.ID == 0 || tok.Name != "ci" || tok.Token == "" || tok.ExpiresAt == nil {
		t.Errorf("created %+v", tok)
	}
	u, err := f.be.UserByAccessToken(f.ctx, tok.Token)
	if err != nil || u.Username() != "reader" {
		t.Errorf("token is for %v, %v", u, err)
	}

	toks, err := r.AccessTokens(f.ctx)
	if err != nil || len(toks) != 1 || toks[0].ID != tok.ID {
		t.Fatalf("tokens = %+v, %v", toks, err)
	}
	if toks, _ := w.AccessTokens(f.ctx); len(toks) != 0 {
		t.Errorf("writer sees %+v", toks)
	}

	// Someone else's token is not found, and stays.
	wantErr(t, w.DeleteAccessToken(f.ctx, tok.ID), proto.ErrTokenNotFound)
	if err := r.DeleteAccessToken(f.ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, r.DeleteAccessToken(f.ctx, tok.ID), proto.ErrTokenNotFound)
}

func TestSetUsername(t *testing.T) {
	f := newFixture(t)
	r := f.as(f.reader)

	wantErr(t, r.SetUsername(f.ctx, "writer"), proto.ErrUserExist)
	if err := r.SetUsername(f.ctx, "1bad"); !IsInvalid(err) {
		t.Errorf("bad name: err = %v", err)
	}
	if err := r.SetUsername(f.ctx, "Reader2"); err != nil {
		t.Fatal(err)
	}
	acct, err := r.Account(f.ctx)
	if err != nil || acct.Username != "reader2" {
		t.Errorf("account = %+v, %v", acct, err)
	}
	// Access follows the account, not the old name. Each SSH command and
	// web request reads the user afresh, as this does.
	u, err := f.be.UserByID(f.ctx, f.reader.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.as(u).Repo(f.ctx, "secret"); err != nil {
		t.Errorf("lost access after rename: %v", err)
	}
}
