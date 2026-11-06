package web

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/golang-jwt/jwt/v5"
	"github.com/matryer/is"
)

// viewFixture is a server with a public repository holding a few files, a
// private one, and the users and tokens to read them as.
type viewFixture struct {
	ctx        context.Context
	be         *backend.Backend
	handler    http.Handler
	ownerToken string
	outpost    *fakeOutpost
}

// fakeOutpost stands in for Outpost's gateway: it signs identity tokens and
// publishes the key that verifies them.
type fakeOutpost struct {
	key     ed25519.PrivateKey
	kid     string
	issuer  string
	fetches atomic.Int32
	srv     *httptest.Server
}

func newFakeOutpost(t *testing.T) *fakeOutpost {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	o := &fakeOutpost{key: priv, kid: "k1", issuer: "https://outpost.example.ts.net"}
	o.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		o.fetches.Add(1)
		pub := o.key.Public().(ed25519.PublicKey)
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{ //nolint:errcheck
			"kty": "OKP", "crv": "Ed25519", "kid": o.kid, "alg": "EdDSA", "use": "sig",
			"x": base64.RawURLEncoding.EncodeToString(pub),
		}}})
	}))
	t.Cleanup(o.srv.Close)
	return o
}

func (o *fakeOutpost) verifier() *outpostVerifier {
	return &outpostVerifier{issuer: o.issuer, jwksURL: o.srv.URL, client: o.srv.Client(), now: time.Now}
}

// token signs an identity token for user at host, adjusted by edit.
func (o *fakeOutpost) token(t *testing.T, user, host string, edit func(*jwt.MapClaims)) string {
	t.Helper()
	now := time.Now()
	c := jwt.MapClaims{
		"iss": o.issuer, "aud": host, "sub": user, "groups": []string{},
		"iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
	}
	if edit != nil {
		edit(&c)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	tok.Header["kid"] = o.kid
	s, err := tok.SignedString(o.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const viewHost = "soft-serve.example.ts.net"

func newViewFixture(t *testing.T) *viewFixture {
	t.Helper()
	is := is.New(t)
	ctx, be, _ := newLFSTestContext(t)

	owner, err := be.CreateUser(ctx, "owner", proto.UserOptions{})
	is.NoErr(err)
	_, err = be.CreateRepository(ctx, "secret", owner, proto.RepositoryOptions{Private: true})
	is.NoErr(err)
	_, err = be.CreateRepository(ctx, "group/open", owner, proto.RepositoryOptions{Description: "An open one"})
	is.NoErr(err)
	_, err = be.CreateUser(ctx, "stranger", proto.UserOptions{})
	is.NoErr(err)
	tok, err := be.CreateAccessToken(ctx, owner, "t", time.Now().Add(time.Hour))
	is.NoErr(err)

	pushFiles(t, filepath.Join(config.FromContext(ctx).DataPath, "repos", "group/open.git"), map[string]string{
		"README.md":        "# Open\n\nSee [the guide](docs/guide.md) and ![logo](img/logo.png).\n\n<script>alert(1)</script>\n",
		"docs/guide.md":    "Guide\n",
		"src/main.go":      "package main\n\nfunc main() {}\n",
		"page.html":        "<script>alert(1)</script>\n",
		"img/logo.png":     "\x89PNG\r\n\x1a\n\x00\x00binary",
		"data.bin":         "\x00\x01\x02\x03",
		"src/deep/file.go": "package deep\n",
	})
	pushFiles(t, filepath.Join(config.FromContext(ctx).DataPath, "repos", "secret.git"), map[string]string{"plans.txt": "top secret\n"})

	o := newFakeOutpost(t)
	return &viewFixture{ctx: ctx, be: be, handler: newRouter(ctx, o.verifier()), ownerToken: tok, outpost: o}
}

// pushFiles commits files to the bare repository at dir, on main.
func pushFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	work := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@example.com", "GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL=a@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range files {
		p := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "First commit\n\nWith a body.")
	run("push", "-q", dir, "main:main")
	// A bare repository made by Soft Serve may point HEAD elsewhere.
	cmd := exec.CommandContext(t.Context(), "git", "--git-dir", dir, "symbolic-ref", "HEAD", "refs/heads/main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("symbolic-ref: %v\n%s", err, out)
	}
}

type req struct {
	method   string
	path     string
	auth     string
	identity string
	header   map[string]string
}

func (f *viewFixture) do(r req) *httptest.ResponseRecorder {
	if r.method == "" {
		r.method = http.MethodGet
	}
	hr := httptest.NewRequestWithContext(context.Background(), r.method, r.path, nil)
	hr.Host = viewHost
	if r.auth != "" {
		hr.Header.Set("Authorization", r.auth)
	}
	if r.identity != "" {
		hr.Header.Set(outpostIdentityHeader, r.identity)
	}
	for k, v := range r.header {
		hr.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, hr)
	return w
}

func repoNames(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var rs []RepoInfo
	if err := json.Unmarshal(w.Body.Bytes(), &rs); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	var names []string
	for _, r := range rs {
		names = append(names, r.Name)
	}
	return names
}

func TestViewAnonymousSeesOnlyPublic(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)

	w := f.do(req{path: "/v1/repos"})
	is.Equal(w.Code, http.StatusOK)
	is.Equal(repoNames(t, w), []string{"group/open"})

	// A private repository answers exactly like a missing one, everywhere.
	for _, p := range []string{"/v1/repos/%s", "/v1/repos/%s/-/tree", "/%s", "/%s/-/commits"} {
		private := f.do(req{path: strings.Replace(p, "%s", "secret", 1)})
		missing := f.do(req{path: strings.Replace(p, "%s", "ghost", 1)})
		is.Equal(private.Code, http.StatusNotFound)
		is.Equal(private.Body.String(), missing.Body.String())
	}
}

func TestViewSoftServeTokenSeesPrivate(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	w := f.do(req{path: "/v1/repos", auth: "Token " + f.ownerToken})
	is.Equal(w.Code, http.StatusOK)
	is.Equal(repoNames(t, w), []string{"group/open", "secret"})

	// A bad credential is refused, not quietly treated as anonymous.
	is.Equal(f.do(req{path: "/v1/repos", auth: "Token nope"}).Code, http.StatusUnauthorized)
	is.Equal(f.do(req{path: "/", auth: "Token nope"}).Code, http.StatusUnauthorized)
}

func TestViewOutpostIdentity(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	o := f.outpost

	// A verified identity is the Soft Serve user of the same name.
	w := f.do(req{path: "/v1/repos", identity: o.token(t, "owner", viewHost, nil)})
	is.Equal(repoNames(t, w), []string{"group/open", "secret"})
	w = f.do(req{path: "/", identity: o.token(t, "owner", viewHost, nil)})
	is.True(strings.Contains(w.Body.String(), "Sign out"))

	// Another user only gets their own access.
	w = f.do(req{path: "/v1/repos", identity: o.token(t, "stranger", viewHost, nil)})
	is.Equal(repoNames(t, w), []string{"group/open"})

	// An Outpost user with no Soft Serve account is told why they see less.
	w = f.do(req{path: "/", identity: o.token(t, "newcomer", viewHost, nil)})
	is.True(strings.Contains(w.Body.String(), "public repositories only"))

	// The plain headers are not trusted: anyone on the machine can send them.
	w = f.do(req{path: "/v1/repos", header: map[string]string{"X-Outpost-User": "owner"}})
	is.Equal(repoNames(t, w), []string{"group/open"})

	// Tokens that don't verify are ignored.
	bad := map[string]string{
		"another host":  o.token(t, "owner", "other.example.ts.net", nil),
		"expired":       o.token(t, "owner", viewHost, func(c *jwt.MapClaims) { (*c)["exp"] = time.Now().Add(-time.Minute).Unix() }),
		"wrong issuer":  o.token(t, "owner", viewHost, func(c *jwt.MapClaims) { (*c)["iss"] = "https://evil.example" }),
		"no expiry":     o.token(t, "owner", viewHost, func(c *jwt.MapClaims) { delete(*c, "exp") }),
		"not signed":    unsignedToken(t, "owner"),
		"wrong key":     wrongKeyToken(t, o, "owner"),
		"garbage":       "not-a-token",
		"empty subject": o.token(t, "", viewHost, nil),
	}
	for name, tok := range bad {
		w := f.do(req{path: "/v1/repos", identity: tok})
		is.Equal(w.Code, http.StatusOK)
		if got := repoNames(t, w); len(got) != 1 || got[0] != "group/open" {
			t.Errorf("%s: got %v; want only the public repository", name, got)
		}
	}
}

func unsignedToken(t *testing.T, user string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": user, "aud": viewHost, "exp": time.Now().Add(time.Minute).Unix()})
	s, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func wrongKeyToken(t *testing.T, o *fakeOutpost, user string) string {
	t.Helper()
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"iss": o.issuer, "sub": user, "aud": viewHost, "exp": time.Now().Add(time.Minute).Unix()})
	tok.Header["kid"] = o.kid
	s, err := tok.SignedString(other)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// An unknown key ID refetches the key set, but not on every request.
func TestOutpostVerifierRateLimitsRefetch(t *testing.T) {
	is := is.New(t)
	o := newFakeOutpost(t)
	v := o.verifier()
	good := o.token(t, "owner", viewHost, nil)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r.Host = viewHost
	r.Header.Set(outpostIdentityHeader, good)
	_, err := v.verify(r)
	is.NoErr(err)
	is.Equal(o.fetches.Load(), int32(1))

	o.kid = "k2" // a forged kid, and the gateway rotated nothing
	r.Header.Set(outpostIdentityHeader, o.token(t, "owner", viewHost, nil))
	for range 5 {
		_, err = v.verify(r)
		is.True(err != nil)
	}
	is.Equal(o.fetches.Load(), int32(1))
}

func TestNoGatewayTrustsNoIdentity(t *testing.T) {
	is := is.New(t)
	t.Setenv(outpostAuthEnv, "")
	v, err := newOutpostVerifier()
	is.NoErr(err)
	is.True(v == nil)

	t.Setenv(outpostAuthEnv, "gateway")
	t.Setenv(outpostIssuerEnv, "")
	_, err = newOutpostVerifier()
	is.True(err != nil) // misconfigured: refuse to start rather than guess
}

func TestViewContent(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	get := func(p string, v any) *httptest.ResponseRecorder {
		t.Helper()
		w := f.do(req{path: p})
		if v != nil {
			if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
				t.Fatalf("%s: %v: %s", p, err, w.Body.String())
			}
		}
		return w
	}

	var repo RepoInfo
	get("/v1/repos/group/open", &repo)
	is.Equal(repo.DefaultBranch, "main")
	is.Equal(repo.Description, "An open one")
	// Behind the gateway git over HTTPS can't authenticate, so only SSH is
	// offered; without it, HTTPS is.
	is.Equal(repo.Clone.HTTPS, "")
	direct := &viewFixture{handler: newRouter(f.ctx, nil)}
	w := direct.do(req{path: "/v1/repos/group/open"})
	is.NoErr(json.Unmarshal(w.Body.Bytes(), &repo))
	is.Equal(repo.Clone.HTTPS, "http://"+viewHost+"/group/open.git")

	var tree Tree
	get("/v1/repos/group/open/-/tree?path=src", &tree)
	is.Equal(len(tree.Entries), 2)
	is.Equal(tree.Entries[0].Type, "dir") // directories first
	is.Equal(tree.Entries[1].Path, "src/main.go")

	var blob Blob
	get("/v1/repos/group/open/-/blob?path=src/main.go", &blob)
	is.Equal(blob.Content, "package main\n\nfunc main() {}\n")
	is.Equal(blob.Path, "src/main.go") // the full path, not just the name
	is.True(!blob.Binary)
	get("/v1/repos/group/open/-/blob?path=data.bin", &blob)
	is.True(blob.Binary)
	is.Equal(blob.Content, "")

	var cp CommitPage
	get("/v1/repos/group/open/-/commits", &cp)
	is.Equal(cp.Total, int64(1))
	is.Equal(cp.Commits[0].Summary, "First commit")

	var cd CommitDetail
	get("/v1/repos/group/open/-/commits/"+cp.Commits[0].SHA, &cd)
	is.Equal(len(cd.Files), 7)
	is.True(strings.Contains(cd.Patch, "+package main"))

	var rd Readme
	get("/v1/repos/group/open/-/readme", &rd)
	is.Equal(rd.Path, "README.md")

	var refs Refs
	get("/v1/repos/group/open/-/refs", &refs)
	is.Equal(refs.DefaultBranch, "main")

	// Bad input is refused before git sees it.
	for _, p := range []string{
		"/v1/repos/group/open/-/tree?ref=--output=/tmp/x",
		"/v1/repos/group/open/-/blob?path=../../etc/passwd",
		"/v1/repos/group/open/-/commits/not-a-sha",
		"/v1/repos/group/open/-/tree?ref=main..HEAD",
	} {
		is.Equal(get(p, nil).Code, http.StatusBadRequest)
	}
	is.Equal(get("/v1/repos/group/open/-/tree?ref=nope", nil).Code, http.StatusNotFound)
	is.Equal(get("/v1/repos/group/open/-/tree?path=src/main.go", nil).Code, http.StatusBadRequest)
	is.Equal(f.do(req{method: http.MethodPost, path: "/v1/repos"}).Code, http.StatusMethodNotAllowed)
}

// Files from a repository never render as pages on our origin.
func TestRawIsNeverADocument(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	for path, want := range map[string]string{
		"page.html":    "text/plain; charset=utf-8",
		"README.md":    "text/plain; charset=utf-8",
		"img/logo.png": "image/png",
		"data.bin":     "application/octet-stream",
	} {
		for _, base := range []string{"/group/open/-/raw", "/v1/repos/group/open/-/raw"} {
			w := f.do(req{path: base + "?path=" + path})
			is.Equal(w.Code, http.StatusOK)
			is.Equal(w.Header().Get("Content-Type"), want)
			is.Equal(w.Header().Get("X-Content-Type-Options"), "nosniff")
			is.True(strings.HasPrefix(w.Header().Get("Content-Security-Policy"), "sandbox"))
		}
	}
	w := f.do(req{path: "/group/open/-/raw?path=data.bin"})
	is.True(strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment"))
}

func TestPages(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	for _, p := range []string{
		"/",
		"/group/open",
		"/group/open/-/tree?path=src",
		"/group/open/-/blob?path=src/main.go",
		"/group/open/-/blob?path=README.md",
		"/group/open/-/blob?path=img/logo.png",
		"/group/open/-/commits",
		"/group/open/-/refs",
	} {
		w := f.do(req{path: p})
		is.Equal(w.Code, http.StatusOK)
		is.True(strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'"))
		// The only host in a page is the one the request came to (in the
		// clone URL); the configured default never leaks in.
		is.True(!strings.Contains(w.Body.String(), "localhost:23232"))
	}

	// The README renders sanitised, with relative links pointed into the repo.
	body := f.do(req{path: "/group/open"}).Body.String()
	is.True(!strings.Contains(body, "<script>alert"))
	is.True(strings.Contains(body, `href="/group/open/-/blob?path=docs%2Fguide.md&amp;ref=main"`))
	is.True(strings.Contains(body, `src="/group/open/-/raw?path=img%2Flogo.png&amp;ref=main"`))

	// A directory opened as a file, and the reverse, redirect.
	is.Equal(f.do(req{path: "/group/open/-/blob?path=src"}).Code, http.StatusFound)
	is.Equal(f.do(req{path: "/group/open/-/tree?path=src/main.go"}).Code, http.StatusFound)
	is.Equal(f.do(req{path: "/group/open.git"}).Code, http.StatusMovedPermanently)

	// Static assets.
	is.Equal(f.do(req{path: "/-/static/style.css"}).Code, http.StatusOK)
	is.Equal(f.do(req{path: "/-/static/code.css"}).Code, http.StatusOK)
}

// The pages take over GETs of repository paths without breaking git or
// go get, which come first.
func TestGitStillRoutes(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	w := f.do(req{path: "/group/open.git/info/refs?service=git-upload-pack"})
	is.Equal(w.Code, http.StatusOK)
	is.Equal(w.Header().Get("Content-Type"), "application/x-git-upload-pack-advertisement")

	w = f.do(req{path: "/group/open?go-get=1"})
	is.Equal(w.Code, http.StatusOK)
	is.True(strings.Contains(w.Body.String(), `name="go-import"`))

	is.Equal(f.do(req{path: "/livez"}).Code, http.StatusOK)
}

func TestMCP(t *testing.T) {
	is := is.New(t)
	f := newViewFixture(t)
	call := func(body string, extra map[string]string) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(body))
		hr.Host = viewHost
		hr.Header.Set("Content-Type", "application/json")
		hr.Header.Set("Accept", "application/json, text/event-stream")
		for k, v := range extra {
			hr.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, hr)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out) //nolint:errcheck
		return w, out
	}
	tool := func(name, args string, extra map[string]string) map[string]any {
		t.Helper()
		_, out := call(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`, extra)
		res, _ := out["result"].(map[string]any)
		if res == nil {
			t.Fatalf("%s: no result: %v", name, out)
		}
		return res
	}

	t.Setenv("OUTPOST_STORAGE_DIR", t.TempDir()) // behind Outpost: Host isn't localhost
	f.handler = newRouter(f.ctx, f.outpost.verifier())

	_, out := call(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	tools, _ := out["result"].(map[string]any)["tools"].([]any)
	is.Equal(len(tools), 8)
	for _, tl := range tools {
		ann := tl.(map[string]any)["annotations"].(map[string]any)
		is.Equal(ann["readOnlyHint"], true)
	}

	res := tool("read_file", `{"repo":"group/open","path":"src/main.go"}`, nil)
	is.Equal(res["structuredContent"].(map[string]any)["content"], "package main\n\nfunc main() {}\n")

	// Access follows the caller, as everywhere else.
	res = tool("read_file", `{"repo":"secret","path":"plans.txt"}`, nil)
	is.Equal(res["isError"], true)
	res = tool("read_file", `{"repo":"secret","path":"plans.txt"}`, map[string]string{outpostIdentityHeader: f.outpost.token(t, "owner", viewHost, nil)})
	is.Equal(res["structuredContent"].(map[string]any)["content"], "top secret\n")

	// A browser on another site can't drive it.
	w, _ := call(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
	is.Equal(w.Code, http.StatusForbidden)

	w, _ = call(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Authorization": "Token nope"})
	is.Equal(w.Code, http.StatusUnauthorized)
}

func TestRenderMarkdown(t *testing.T) {
	is := is.New(t)
	target := func(p string, image bool) string {
		if image {
			return "/r/-/raw?path=" + p
		}
		return "/r/-/blob?path=" + p
	}
	out := string(renderMarkdown("[a](b.md#x) [ext](https://example.com) [top](#t) <img src=\"pic.png\"> <a href=\"javascript:alert(1)\">x</a> <iframe src=\"x\"></iframe>", "docs", target))
	is.True(strings.Contains(out, `href="/r/-/blob?path=docs/b.md#x"`))
	is.True(strings.Contains(out, `href="https://example.com"`))
	is.True(strings.Contains(out, `href="#t"`))
	is.True(strings.Contains(out, `src="/r/-/raw?path=docs/pic.png"`))
	is.True(!strings.Contains(out, "javascript:"))
	is.True(!strings.Contains(out, "<iframe"))

	// Links can't climb out of the repository.
	out = string(renderMarkdown("[up](../../x)", "", target))
	is.True(strings.Contains(out, `href="../../x"`))
}

func TestBaseURL(t *testing.T) {
	is := is.New(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r.Host = viewHost
	is.Equal(baseURL(r), "http://"+viewHost)
	r.Header.Set("X-Forwarded-Proto", "https")
	is.Equal(baseURL(r), "https://"+viewHost)
	r.Header.Set("X-Forwarded-Host", "evil/path")
	is.Equal(baseURL(r), "") // not a host: no URL rather than a wrong one
}
