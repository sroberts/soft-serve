package web

import (
	_ "embed"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	gopath "path"
	"strconv"
	"strings"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/ops"
)

// The REST API: JSON under /v1, described by /v1/openapi.json. Reading is
// here; managing repositories is in api_ops.go.
//
// Repository names may contain slashes, so the name and the action are
// separated by "/-/": /v1/repos/group/project/-/tree?ref=main&path=docs.

//go:embed openapi.json
var openAPIDoc []byte

// apiHandler serves /v1/.
type apiHandler struct {
	verifier *outpostVerifier
}

func (h apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/openapi.json" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(openAPIDoc) //nolint:errcheck
		return
	}

	c, err := identifyCaller(r, h.verifier)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="Soft Serve" charset="UTF-8", Token`)
		writeAPIError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if rest, ok := strings.CutPrefix(r.URL.Path, "/v1/repos"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		svc := ops.NewLocal(backend.FromContext(r.Context()), ops.UserActor(c.user))
		if serveOps(w, r, svc, c.user == nil, strings.Trim(rest, "/")) {
			return
		}
	}
	if serveAdmin(w, r, ops.NewLocal(backend.FromContext(r.Context()), ops.UserActor(c.user)), c.user == nil) {
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/v1/me"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		svc := ops.NewLocal(backend.FromContext(r.Context()), ops.UserActor(c.user))
		if serveAccount(w, r, svc, c.user == nil, strings.TrimRight(rest, "/")) {
			return
		}
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	b := newBrowser(r.Context(), c.user, cloneBase(r, h.verifier))

	if r.URL.Path == "/v1/me" {
		h.respond(w, r)(b.account(r.Context(), c.outpostUser))
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/v1/repos")
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	rest = strings.Trim(rest, "/")
	if rest == "" {
		rs, err := b.listRepos(r.Context(), r.URL.Query().Get("hidden") == "true")
		h.respond(w, r)(filterRepos(rs, r.URL.Query().Get("q")), err)
		return
	}
	name, action, _ := strings.Cut(rest, "/-/")
	q := r.URL.Query()
	ctx := r.Context()
	switch {
	case action == "":
		h.respond(w, r)(b.repo(ctx, name))
	case action == "refs":
		h.respond(w, r)(b.refs(ctx, name))
	case action == "commits":
		page, _ := strconv.Atoi(q.Get("page"))
		perPage, _ := strconv.Atoi(q.Get("per_page"))
		f := commitFilter{Path: q.Get("path"), Query: q.Get("q"), Author: q.Get("author")}
		h.respond(w, r)(b.commits(ctx, name, q.Get("ref"), f, page, perPage))
	case strings.HasPrefix(action, "commits/"):
		h.respond(w, r)(b.commit(ctx, name, strings.TrimPrefix(action, "commits/"), apiDiffOpts(q)))
	case action == "tree":
		h.respond(w, r)(b.tree(ctx, name, q.Get("ref"), q.Get("path"), q.Get("last_commits") == "true"))
	case action == "blame":
		h.respond(w, r)(b.blame(ctx, name, q.Get("ref"), q.Get("path")))
	case action == "compare":
		h.respond(w, r)(b.compare(ctx, name, q.Get("base"), q.Get("head"), apiDiffOpts(q)))
	case action == "search":
		h.respond(w, r)(b.search(ctx, name, q.Get("ref"), q.Get("q")))
	case action == "files":
		h.respond(w, r)(b.findFiles(ctx, name, q.Get("ref"), q.Get("q")))
	case action == "stats":
		h.respond(w, r)(b.stats(ctx, name, q.Get("ref")))
	case action == "collaborators":
		h.respond(w, r)(b.collaborators(ctx, name))
	case action == "archive":
		serveArchive(w, r, b, name)
	case action == "blob":
		h.respond(w, r)(b.blob(ctx, name, q.Get("ref"), q.Get("path")))
	case action == "readme":
		h.respond(w, r)(b.readme(ctx, name, q.Get("ref")))
	case action == "raw":
		serveRaw(w, r, b, name)
	default:
		writeAPIError(w, http.StatusNotFound, "not found")
	}
}

// apiDiffOpts reads how a diff is asked for: ignore_whitespace=true, and
// path for one file's alone.
func apiDiffOpts(q url.Values) diffOpts {
	return diffOpts{IgnoreWhitespace: q.Get("ignore_whitespace") == "true", Path: q.Get("path")}
}

// respond writes v as JSON, or the error.
func (h apiHandler) respond(w http.ResponseWriter, r *http.Request) func(any, error) {
	return func(v any, err error) {
		if err != nil {
			if statusFor(err) == http.StatusInternalServerError {
				log.FromContext(r.Context()).Error("api", "path", r.URL.Path, "err", err)
			}
			writeAPIError(w, statusFor(err), publicMessage(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(v) //nolint:errcheck
	}
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

// serveRaw streams a file. Repository content is untrusted, and it is served
// on the same origin as the pages, so it is never rendered as a document:
// text goes out as text/plain, images that can't carry script as themselves,
// and everything else as a download, all with nosniff and a sandbox.
func serveRaw(w http.ResponseWriter, r *http.Request, b *browser, name string) {
	q := r.URL.Query()
	wrote := false
	err := b.raw(r.Context(), name, q.Get("ref"), q.Get("path"), w, func(p string, size int64, binary bool) bool {
		wrote = true
		hdr := w.Header()
		hdr.Set("Content-Type", rawContentType(p, binary))
		hdr.Set("Content-Length", strconv.FormatInt(size, 10))
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
		hdr.Set("Cache-Control", "private, max-age=60")
		if strings.HasPrefix(hdr.Get("Content-Type"), "application/octet-stream") {
			hdr.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": gopath.Base(p)}))
		}
		return r.Method != http.MethodHead
	})
	if err != nil && !wrote {
		if statusFor(err) == http.StatusInternalServerError {
			log.FromContext(r.Context()).Error("raw", "path", r.URL.Path, "err", err)
		}
		writeAPIError(w, statusFor(err), publicMessage(err))
		return
	}
	if err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		log.FromContext(r.Context()).Debug("raw: stream ended early", "err", err)
	}
}

// serveArchive streams a snapshot of a revision as a download.
func serveArchive(w http.ResponseWriter, r *http.Request, b *browser, name string) {
	q := r.URL.Query()
	wrote := false
	err := b.archive(r.Context(), name, q.Get("ref"), q.Get("format"), w, func(filename, contentType string) bool {
		wrote = true
		hdr := w.Header()
		hdr.Set("Content-Type", contentType)
		hdr.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		hdr.Set("Cache-Control", "private, no-store")
		// HEAD gets the headers without the cost of building the archive.
		return r.Method != http.MethodHead
	})
	if err != nil && !wrote {
		if statusFor(err) == http.StatusInternalServerError {
			log.FromContext(r.Context()).Error("archive", "path", r.URL.Path, "err", err)
		}
		writeAPIError(w, statusFor(err), publicMessage(err))
		return
	}
	if err != nil {
		// Headers are gone; the client sees a cut-off download.
		log.FromContext(r.Context()).Warn("archive: stream failed", "path", r.URL.Path, "err", err)
	}
}

// rawContentType picks a safe type for a file served from a repository.
func rawContentType(p string, binary bool) string {
	switch strings.ToLower(gopath.Ext(p)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".ico":
		return "image/x-icon"
	}
	if binary {
		return "application/octet-stream"
	}
	// SVG and HTML can carry script; like any other text they are shown as
	// text, never rendered.
	return "text/plain; charset=utf-8"
}
