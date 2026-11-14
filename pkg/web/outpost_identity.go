package web

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Behind Outpost's login gateway (OUTPOST_AUTH=gateway), every request
// carries X-Outpost-Identity: an EdDSA-signed JWT naming the signed-in user,
// whose keys are published at OUTPOST_JWKS_URL. The plain X-Outpost-User
// header is not trusted for anything: any process on this machine can
// connect to our loopback port and set it, and reading a private repository
// is something a neighbour must not be able to do as someone else. See
// outpost-service-spec.md §3.

const (
	outpostAuthEnv   = "OUTPOST_AUTH"
	outpostIssuerEnv = "OUTPOST_ISSUER"
	outpostJWKSEnv   = "OUTPOST_JWKS_URL"

	outpostIdentityHeader = "X-Outpost-Identity"

	// jwksRefreshEvery bounds how often an unknown key ID makes us fetch the
	// key set again, so a stream of forged tokens can't hammer the gateway.
	jwksRefreshEvery = 10 * time.Second
	// jwksTimeout bounds one fetch of the key set.
	jwksTimeout = 3 * time.Second
	// tokenLeeway allows for clock skew; Outpost's tokens live 60 seconds.
	tokenLeeway = 5 * time.Second
)

// outpostVerifier checks Outpost identity tokens.
type outpostVerifier struct {
	issuer  string
	jwksURL string
	client  *http.Client
	now     func() time.Time

	// mu guards the fields below. It is never held across a fetch, so a
	// slow key server can't stall requests whose key is already known.
	mu      sync.Mutex
	keys    map[string]ed25519.PublicKey
	fetched time.Time
	// fetching is closed when the fetch in flight, if any, ends.
	fetching chan struct{}
}

// newOutpostVerifier returns a verifier when Outpost's gateway fronts us, or
// nil when it does not, in which case no X-Outpost-* header is trusted.
func newOutpostVerifier() (*outpostVerifier, error) {
	if os.Getenv(outpostAuthEnv) != "gateway" {
		return nil, nil
	}
	issuer, jwks := os.Getenv(outpostIssuerEnv), os.Getenv(outpostJWKSEnv)
	if issuer == "" || jwks == "" {
		return nil, fmt.Errorf("%s=gateway needs %s and %s", outpostAuthEnv, outpostIssuerEnv, outpostJWKSEnv)
	}
	return &outpostVerifier{
		issuer:  issuer,
		jwksURL: jwks,
		client:  &http.Client{Timeout: jwksTimeout},
		now:     time.Now,
	}, nil
}

// outpostClaims is the X-Outpost-Identity payload.
type outpostClaims struct {
	jwt.RegisteredClaims
	Name   string   `json:"name"`
	Email  string   `json:"email"`
	Groups []string `json:"groups"`
}

var errNoIdentity = errors.New("no outpost identity")

// verify checks r's identity token and returns the Outpost username.
func (v *outpostVerifier) verify(r *http.Request) (string, error) {
	id, err := v.verifyIdentity(r)
	return id.name, err
}

// outpostIdentity is who a verified identity token names.
type outpostIdentity struct {
	name  string
	email string
}

// verifyIdentity checks r's identity token and returns who it names.
func (v *outpostVerifier) verifyIdentity(r *http.Request) (outpostIdentity, error) {
	tok := r.Header.Get(outpostIdentityHeader)
	if tok == "" {
		return outpostIdentity{}, errNoIdentity
	}
	claims := &outpostClaims{}
	_, err := jwt.ParseWithClaims(tok, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return v.key(r.Context(), kid)
	},
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(tokenLeeway),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil {
		return outpostIdentity{}, err
	}
	// The audience is the host the request came to; Host is preserved
	// through the gateway. X-Forwarded-Host is the client's to set, so it
	// must not count, or a token minted for another host would pass.
	if !audienceMatches(claims.Audience, requestHosts(r)) {
		return outpostIdentity{}, errors.New("identity token is for another host")
	}
	if claims.Subject == "" {
		return outpostIdentity{}, errors.New("identity token names no user")
	}
	return outpostIdentity{name: claims.Subject, email: strings.TrimSpace(claims.Email)}, nil
}

// requestHosts is the name r was addressed to, without a port.
func requestHosts(r *http.Request) []string {
	h := r.Host
	if h == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return []string{strings.ToLower(h)}
}

func audienceMatches(aud jwt.ClaimStrings, hosts []string) bool {
	for _, a := range aud {
		a = strings.ToLower(a)
		if host, _, err := net.SplitHostPort(a); err == nil {
			a = host
		}
		for _, h := range hosts {
			if a == h {
				return true
			}
		}
	}
	return false
}

// key returns the public key with ID kid, fetching the key set when it isn't
// known yet.
func (v *outpostVerifier) key(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	for {
		v.mu.Lock()
		if k, ok := v.keys[kid]; ok {
			v.mu.Unlock()
			return k, nil
		}
		if wait := v.fetching; wait != nil {
			// Someone is already fetching: wait for that rather than
			// fetching again, then look once more.
			v.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if !v.fetched.IsZero() && v.now().Sub(v.fetched) < jwksRefreshEvery {
			v.mu.Unlock()
			return nil, fmt.Errorf("unknown signing key %q", kid)
		}
		v.fetched = v.now()
		done := make(chan struct{})
		v.fetching = done
		v.mu.Unlock()

		// The fetch has its own timeout; others may be waiting on it, so
		// this request going away shouldn't cut it short.
		keys, err := v.fetch(context.WithoutCancel(ctx))

		v.mu.Lock()
		if err == nil {
			v.keys = keys
		}
		k, ok := v.keys[kid]
		v.fetching = nil
		close(done)
		v.mu.Unlock()
		switch {
		case err != nil:
			return nil, fmt.Errorf("fetch outpost keys: %w", err)
		case ok:
			return k, nil
		default:
			return nil, fmt.Errorf("unknown signing key %q", kid)
		}
	}
}

func (v *outpostVerifier) fetch(ctx context.Context) (map[string]ed25519.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, jwksTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&set); err != nil {
		return nil, err
	}
	keys := map[string]ed25519.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			continue
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			continue
		}
		keys[k.Kid] = ed25519.PublicKey(x)
	}
	return keys, nil
}
