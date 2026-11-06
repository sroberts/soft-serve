package web

import (
	"context"
	"net/http"
	"os"

	"github.com/charmbracelet/soft-serve/pkg/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP over the Streamable HTTP transport at /mcp: read-only tools over the
// same view as the web pages and the REST API, answering with what the
// caller may read.

type callerKey struct{}

// mcpHandler serves /mcp.
func mcpHandler(v *outpostVerifier) http.Handler {
	cache := mcp.NewSchemaCache()
	stream := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		c, _ := r.Context().Value(callerKey{}).(caller)
		return newMCPServer(newBrowser(r.Context(), c.user, cloneBase(r, v)), cache)
	}, &mcp.StreamableHTTPOptions{
		// Each request stands alone, so a restart loses nothing, and there
		// is no session state for a portal that drops cookies to break.
		Stateless:    true,
		JSONResponse: true,
		// Outpost reaches us on 127.0.0.1 with the tailnet name in Host,
		// which this check would refuse. Behind Outpost only its node and
		// gateway can reach us, and the origin check below still stops
		// browsers on other sites.
		DisableLocalhostProtection: os.Getenv("OUTPOST_STORAGE_DIR") != "",
		MaxRequestBodyBytes:        1 << 20,
	})
	h := http.NewCrossOriginProtection().Handler(stream)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := identifyCaller(r, v)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, c)))
	})
}

const mcpInstructions = `Read-only access to the Git repositories on this Soft Serve server.
Start with list_repositories. A repository name may contain slashes. Every
tool that takes a ref accepts a branch, a tag or a commit hash, and uses the
default branch when it is left out. Files and patches longer than 1 MiB are
cut off, and say so.`

func readOnly(title string) *mcp.ToolAnnotations {
	f := false
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: &f, OpenWorldHint: &f}
}

type repoArg struct {
	Repo string `json:"repo" jsonschema:"repository name, as list_repositories gives it"`
}

type refArgs struct {
	Repo string `json:"repo" jsonschema:"repository name, as list_repositories gives it"`
	Ref  string `json:"ref,omitempty" jsonschema:"branch, tag or commit hash; the default branch when omitted"`
}

type pathArgs struct {
	Repo string `json:"repo" jsonschema:"repository name, as list_repositories gives it"`
	Ref  string `json:"ref,omitempty" jsonschema:"branch, tag or commit hash; the default branch when omitted"`
	Path string `json:"path,omitempty" jsonschema:"path inside the repository; the root when omitted"`
}

type commitsArgs struct {
	Repo    string `json:"repo" jsonschema:"repository name, as list_repositories gives it"`
	Ref     string `json:"ref,omitempty" jsonschema:"branch, tag or commit hash; the default branch when omitted"`
	Path    string `json:"path,omitempty" jsonschema:"only commits that touched this file or directory"`
	Page    int    `json:"page,omitempty" jsonschema:"page number, from 1"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"commits per page, up to 100; 30 when omitted"`
}

type commitArgs struct {
	Repo string `json:"repo" jsonschema:"repository name, as list_repositories gives it"`
	SHA  string `json:"sha" jsonschema:"full or abbreviated commit hash"`
}

type repoList struct {
	Repositories []RepoInfo `json:"repositories"`
}

// tool adapts a browse call to an MCP tool handler.
func tool[In, Out any](f func(context.Context, In) (Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := f(ctx, in)
		if err != nil {
			var zero Out
			if statusFor(err) == http.StatusInternalServerError {
				return nil, zero, errInternal
			}
			return nil, zero, err
		}
		return nil, out, nil
	}
}

var errInternal = mcpError("internal error")

type mcpError string

func (e mcpError) Error() string { return string(e) }

func newMCPServer(b *browser, cache *mcp.SchemaCache) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "soft-serve", Title: b.cfg.Name, Version: version.Version}, &mcp.ServerOptions{
		Instructions: mcpInstructions,
		SchemaCache:  cache,
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_repositories",
		Description: "List the repositories you can read, with their descriptions, default branches and clone URLs.",
		Annotations: readOnly("List repositories"),
	}, tool(func(ctx context.Context, _ struct{}) (repoList, error) {
		rs, err := b.repos(ctx)
		return repoList{Repositories: rs}, err
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_repository",
		Description: "Describe one repository: description, privacy, default branch and clone URLs.",
		Annotations: readOnly("Get repository"),
	}, tool(func(ctx context.Context, in repoArg) (RepoInfo, error) {
		return b.repo(ctx, in.Repo)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_readme",
		Description: "Read a repository's README, from the root, .github or docs.",
		Annotations: readOnly("Get README"),
	}, tool(func(ctx context.Context, in refArgs) (Readme, error) {
		return b.readme(ctx, in.Repo, in.Ref)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_refs",
		Description: "List a repository's branches and tags, each with the commit it points at.",
		Annotations: readOnly("List branches and tags"),
	}, tool(func(ctx context.Context, in repoArg) (Refs, error) {
		return b.refs(ctx, in.Repo)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_commits",
		Description: "List the commits reachable from a ref, newest first, one page at a time. Give a path for the history of one file or directory.",
		Annotations: readOnly("List commits"),
	}, tool(func(ctx context.Context, in commitsArgs) (CommitPage, error) {
		return b.commits(ctx, in.Repo, in.Ref, in.Path, in.Page, in.PerPage)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_commit",
		Description: "Get one commit: its message, the files it changed and its unified diff.",
		Annotations: readOnly("Get commit"),
	}, tool(func(ctx context.Context, in commitArgs) (CommitDetail, error) {
		return b.commit(ctx, in.Repo, in.SHA)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_directory",
		Description: "List a directory in a repository at a ref: names, types (dir, file, symlink, submodule) and sizes.",
		Annotations: readOnly("List directory"),
	}, tool(func(ctx context.Context, in pathArgs) (Tree, error) {
		return b.tree(ctx, in.Repo, in.Ref, in.Path)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "read_file",
		Description: "Read a text file in a repository at a ref. Binary files are reported as binary, without content.",
		Annotations: readOnly("Read file"),
	}, tool(func(ctx context.Context, in pathArgs) (Blob, error) {
		return b.blob(ctx, in.Repo, in.Ref, in.Path)
	}))

	return s
}
