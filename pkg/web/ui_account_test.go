package web

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/soft-serve/pkg/proto"
	gossh "golang.org/x/crypto/ssh"
)

// newKey is a fresh SSH key in authorized_keys form, and its fingerprint.
func newKey(t *testing.T) (string, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(gossh.MarshalAuthorizedKey(pk))) + " me@laptop", gossh.FingerprintSHA256(pk)
}

// esc is s as html/template writes it in text and attributes.
var esc = strings.NewReplacer("'", "&#39;", "+", "&#43;", `"`, "&#34;").Replace

func wantStatus(t *testing.T, w interface{ Result() *http.Response }, want int, what string) {
	t.Helper()
	if got := w.Result().StatusCode; got != want {
		t.Errorf("%s: status %d, want %d", what, got, want)
	}
}

func TestUIAccountKeys(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()
	key, fp := newKey(t)

	w := f.do(req{path: "/-/me", auth: auth})
	has(t, w, `action="/-/me/keys"`, `<textarea name="key"`, "No SSH keys yet.")

	loc := seeOther(t, f.post("/-/me/keys", auth, url.Values{"key": {key}}), "/-/me?done=key-added")
	w = f.do(req{path: loc, auth: auth})
	has(t, w, "SSH key added.", esc(fp), `name="fingerprint" value="`+esc(fp)+`"`, "ssh-ed25519 AAAAC3NzaC1l…", `title="`+esc(strings.TrimSuffix(key, " me@laptop"))+`"`)

	// The same key again, a key someone else has, and something that
	// isn't a key all fail on the form, which keeps what was pasted.
	w = f.post("/-/me/keys", auth, url.Values{"key": {key}})
	wantStatus(t, w, http.StatusConflict, "duplicate key")
	has(t, w, "You already have this public key.", `class="formerror" role="alert"`)
	other, _ := newKey(t)
	stranger, _ := f.be.User(f.ctx, "stranger")
	pk, _, _, _, _ := gossh.ParseAuthorizedKey([]byte(other))
	if err := f.be.AddPublicKey(f.ctx, stranger.Username(), pk); err != nil {
		t.Fatal(err)
	}
	w = f.post("/-/me/keys", auth, url.Values{"key": {other}})
	wantStatus(t, w, http.StatusConflict, "someone else's key")
	has(t, w, "Public key belongs to another account.")
	w = f.post("/-/me/keys", auth, url.Values{"key": {"not a key"}})
	wantStatus(t, w, http.StatusBadRequest, "bad key")
	has(t, w, "Not an SSH public key", ">not a key</textarea>")

	// Removing it by fingerprint; then it is gone.
	seeOther(t, f.post("/-/me/keys/remove", auth, url.Values{"fingerprint": {fp}}), "/-/me?done=key-removed")
	if u, _ := f.be.User(f.ctx, "owner"); len(u.PublicKeys()) != 0 {
		t.Errorf("owner still has %d keys", len(u.PublicKeys()))
	}
	w = f.post("/-/me/keys/remove", auth, url.Values{"fingerprint": {fp}})
	wantStatus(t, w, http.StatusNotFound, "remove twice")
	has(t, w, esc("That key isn't on your account."))
	// Someone else's key can't be removed by naming it.
	wantStatus(t, f.post("/-/me/keys/remove", auth, url.Values{"fingerprint": {gossh.FingerprintSHA256(pk)}}), http.StatusNotFound, "remove someone else's key")
	if u, _ := f.be.User(f.ctx, "stranger"); len(u.PublicKeys()) != 1 {
		t.Error("removed someone else's key")
	}

	// A GET to a form's address goes back to the page.
	seeOther(t, f.do(req{path: "/-/me/keys", auth: auth}), "/-/me")
	wantStatus(t, f.post("/-/me/nope", auth, url.Values{}), http.StatusNotFound, "unknown action")
}

var tokenRE = regexp.MustCompile(`<code class="token">([^<]+)</code>`)

func TestUIAccountTokens(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()

	w := f.do(req{path: "/-/me", auth: auth})
	has(t, w, `action="/-/me/tokens"`, `<option value="30" selected>In 30 days</option>`, `<option value="never">Never</option>`)

	// The token is in the answer to the post, and nowhere else: no
	// redirect, no store.
	w = f.post("/-/me/tokens", auth, url.Values{"name": {"laptop"}, "expires": {"90"}})
	wantStatus(t, w, http.StatusOK, "create token")
	if loc := w.Header().Get("Location"); loc != "" {
		t.Errorf("create token redirected to %q", loc)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", cc)
	}
	has(t, w, "Your new token: laptop", "It won't be shown again.")
	m := tokenRE.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no token on the page:\n%s", w.Body)
	}
	tok := m[1]
	for _, h := range w.Header() {
		for _, v := range h {
			if strings.Contains(v, tok) {
				t.Errorf("a header carries the token: %q", v)
			}
		}
	}
	// It works, and it expires when chosen.
	u, err := f.be.UserByAccessToken(f.ctx, tok)
	if err != nil || u.Username() != "owner" {
		t.Fatalf("new token: %v", err)
	}
	owner, _ := f.be.User(f.ctx, "owner")
	toks, _ := f.be.ListAccessTokens(f.ctx, owner)
	var made proto.AccessToken
	for _, at := range toks {
		if at.Name == "laptop" {
			made = at
		}
	}
	if d := time.Until(made.ExpiresAt); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Errorf("expires in %v, want 90 days", d)
	}
	// The page never shows it again.
	w = f.do(req{path: "/-/me", auth: auth})
	hasNot(t, w, tok, `class="token"`)
	has(t, w, `<span class="refname">laptop</span>`, `name="id" value="`+strconv.FormatInt(made.ID, 10)+`"`)

	// Never expiring; a bad choice; no name.
	w = f.post("/-/me/tokens", auth, url.Values{"name": {"ci"}, "expires": {"never"}})
	wantStatus(t, w, http.StatusOK, "never-expiring token")
	has(t, w, "Your new token: ci")
	w = f.post("/-/me/tokens", auth, url.Values{"name": {"x"}, "expires": {"1000"}})
	wantStatus(t, w, http.StatusBadRequest, "bad expiry")
	has(t, w, "Choose when the token expires.", `value="x"`)
	hasNot(t, w, `class="token"`)
	wantStatus(t, f.post("/-/me/tokens", auth, url.Values{"name": {" "}, "expires": {"7"}}), http.StatusBadRequest, "no name")

	// An expired token is shown as one.
	if _, err := f.be.CreateAccessToken(f.ctx, owner, "old", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	has(t, f.do(req{path: "/-/me", auth: auth}), `<span class="badge bad">expired</span>`)

	// Someone else's token is not found, and stays.
	stranger, _ := f.be.User(f.ctx, "stranger")
	if _, err := f.be.CreateAccessToken(f.ctx, stranger, "theirs", time.Time{}); err != nil {
		t.Fatal(err)
	}
	theirs, _ := f.be.ListAccessTokens(f.ctx, stranger)
	w = f.post("/-/me/tokens/delete", auth, url.Values{"id": {strconv.FormatInt(theirs[0].ID, 10)}})
	wantStatus(t, w, http.StatusNotFound, "delete someone else's token")
	has(t, w, esc("That token doesn't exist, or isn't yours."))
	if left, _ := f.be.ListAccessTokens(f.ctx, stranger); len(left) != 1 {
		t.Error("deleted someone else's token")
	}
	wantStatus(t, f.post("/-/me/tokens/delete", auth, url.Values{"id": {"abc"}}), http.StatusNotFound, "bad token ID")

	// Deleting your own.
	seeOther(t, f.post("/-/me/tokens/delete", auth, url.Values{"id": {strconv.FormatInt(made.ID, 10)}}), "/-/me?done=token-deleted")
	if _, err := f.be.UserByAccessToken(f.ctx, tok); err == nil {
		t.Error("deleted token still works")
	}
}

func TestUIAccountUsername(t *testing.T) {
	f := newViewFixture(t)
	auth := f.owner()

	has(t, f.do(req{path: "/-/me", auth: auth}), `action="/-/me/username"`, `name="username" value="owner"`)

	w := f.post("/-/me/username", auth, url.Values{"username": {"stranger"}})
	wantStatus(t, w, http.StatusConflict, "taken name")
	has(t, w, "Username is already taken.", `name="username" value="stranger"`)
	w = f.post("/-/me/username", auth, url.Values{"username": {"9lives"}})
	wantStatus(t, w, http.StatusBadRequest, "bad name")
	has(t, w, "Username must start with a letter.")

	loc := seeOther(t, f.post("/-/me/username", auth, url.Values{"username": {"boss"}}), "/-/me?done=username-changed")
	// The token still signs them in, under the new name.
	has(t, f.do(req{path: loc, auth: auth}), "Username changed.", "<strong>boss</strong>")
	if _, err := f.be.User(f.ctx, "boss"); err != nil {
		t.Errorf("no user boss after rename: %v", err)
	}
}

func TestUIAccountOutpost(t *testing.T) {
	f := newViewFixture(t)
	id := f.outpost.token(t, "owner", viewHost, nil)

	// Signed in by Outpost, which maps by name: no rename form, and why.
	w := f.do(req{path: "/-/me", identity: id})
	has(t, w, "You signed in through Outpost", "set-username &lt;new-name&gt;", `action="/-/me/keys"`, `action="/-/me/tokens"`)
	hasNot(t, w, `action="/-/me/username"`)

	w = f.do(req{method: http.MethodPost, path: "/-/me/username", identity: id, body: "username=boss",
		header: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}})
	wantStatus(t, w, http.StatusForbidden, "outpost rename")
	has(t, w, esc("can't rename it here"))
	if _, err := f.be.User(f.ctx, "owner"); err != nil {
		t.Error("renamed through Outpost")
	}

	// Their own credentials, even through the gateway, are not Outpost's
	// mapping, so they may rename.
	has(t, f.do(req{path: "/-/me", identity: id, auth: f.owner()}), `action="/-/me/username"`)
}

func TestUIAccountAnonymous(t *testing.T) {
	f := newViewFixture(t)
	key, _ := newKey(t)

	w := f.do(req{path: "/-/me"})
	hasNot(t, w, "<form", "<textarea")

	for path, form := range map[string]url.Values{
		"/-/me/keys":          {"key": {key}},
		"/-/me/keys/remove":   {"fingerprint": {"SHA256:x"}},
		"/-/me/tokens":        {"name": {"x"}, "expires": {"never"}},
		"/-/me/tokens/delete": {"id": {"1"}},
		"/-/me/username":      {"username": {"x"}},
	} {
		w := f.post(path, "", form)
		wantStatus(t, w, http.StatusUnauthorized, "anonymous "+path)
		has(t, w, "Sign in to do that.")
		hasNot(t, w, `class="token"`)
	}

	// An Outpost user with no account here is refused the same way.
	nobody := f.outpost.token(t, "nobody", viewHost, nil)
	hasNot(t, f.do(req{path: "/-/me", identity: nobody}), `action="/-/me`)
	w = f.do(req{method: http.MethodPost, path: "/-/me/tokens", identity: nobody, body: "name=x&expires=never",
		header: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}})
	wantStatus(t, w, http.StatusUnauthorized, "outpost user without an account")
}
