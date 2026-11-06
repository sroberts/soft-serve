package web

import (
	"bytes"
	"html/template"
	"net/url"
	gopath "path"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Rendering repository content for the web pages. Everything in a repository
// is untrusted: Markdown is rendered and then sanitized, and code is
// highlighted from escaped tokens.

// maxHighlightBytes is the largest file the file page highlights; bigger
// text is shown plain.
const maxHighlightBytes = 256 << 10

// linkTarget says where a relative link in a README points.
type linkTarget func(p string, image bool) string

// renderMarkdown renders a README at dir in the repository. Relative links
// and images are pointed at the file pages and the raw endpoint.
func renderMarkdown(src, dir string, target linkTarget) template.HTML {
	resolve := func(dest string, image bool) string {
		u, err := url.Parse(dest)
		if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "#") {
			return dest
		}
		p := gopath.Join(dir, u.Path)
		if strings.HasPrefix(p, "..") {
			return dest
		}
		out := target(p, image)
		if u.Fragment != "" {
			out += "#" + u.Fragment
		}
		return out
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(linkRewriter(resolve), 100)),
		),
		// Raw HTML is common in READMEs; the sanitizer below decides what
		// of it survives.
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src)) //nolint:gosec // escaped
	}

	policy := bluemonday.UGCPolicy()
	policy.AllowAttrs("id").OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	policy.AllowAttrs("align").OnElements("p", "div", "img", "td", "th")
	policy.RequireNoReferrerOnLinks(true)
	policy.AddTargetBlankToFullyQualifiedLinks(true)
	// Images written as raw HTML aren't in the Markdown tree, so their
	// relative sources are rewritten here.
	policy.RewriteSrc(func(u *url.URL) {
		if u.Scheme == "" && u.Host == "" && u.Path != "" && !strings.HasPrefix(u.Path, "/") {
			if nu, err := url.Parse(resolve(u.String(), true)); err == nil {
				*u = *nu
			}
		}
	})
	return template.HTML(policy.SanitizeBytes(buf.Bytes())) //nolint:gosec // sanitized
}

// linkRewriter points relative Markdown links and images into the repository.
type linkRewriter func(dest string, image bool) string

func (f linkRewriter) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Link:
			v.Destination = []byte(f(string(v.Destination), false))
		case *ast.Image:
			v.Destination = []byte(f(string(v.Destination), true))
		}
		return ast.WalkContinue, nil
	})
}

// codeFormatter writes the stylesheet for the classes highlightLines uses.
var codeFormatter = chromahtml.New(chromahtml.WithClasses(true))

// highlightLines colours a file's text and returns it a line at a time, for
// pages that interleave code with other columns (blame). The classes are
// the ones highlightCSS styles; wrap the lines in an element of class chroma.
func highlightLines(name, src string) []template.HTML {
	text := strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	out := make([]template.HTML, len(text))
	if len(src) <= maxHighlightBytes {
		lexer := lexers.Match(name)
		if lexer == nil {
			lexer = lexers.Analyse(src)
		}
		if lexer != nil {
			if it, err := chroma.Coalesce(lexer).Tokenise(nil, src); err == nil {
				lines := chroma.SplitTokensIntoLines(it.Tokens())
				if len(lines) >= len(text) {
					for i := range out {
						var b strings.Builder
						for _, tok := range lines[i] {
							v := strings.TrimSuffix(tok.Value, "\n")
							if v == "" {
								continue
							}
							if cls := tokenClass(tok.Type); cls != "" {
								b.WriteString(`<span class="` + cls + `">` + template.HTMLEscapeString(v) + `</span>`)
							} else {
								b.WriteString(template.HTMLEscapeString(v))
							}
						}
						out[i] = template.HTML(b.String()) //nolint:gosec // tokens escaped
					}
					return out
				}
			}
		}
	}
	for i, l := range text {
		out[i] = template.HTML(template.HTMLEscapeString(l)) //nolint:gosec // escaped
	}
	return out
}

// tokenClass is the CSS class chroma's HTML formatter gives a token type.
func tokenClass(t chroma.TokenType) string {
	for ; t != 0; t = t.Parent() {
		if cls, ok := chroma.StandardTypes[t]; ok {
			return cls
		}
	}
	return chroma.StandardTypes[t]
}

var (
	codeCSSOnce sync.Once
	codeCSS     []byte
)

// highlightCSS is the colouring for highlighted code, light and dark.
func highlightCSS() []byte {
	codeCSSOnce.Do(func() {
		// Each scheme gets only its own rules: a dark style layered over a
		// light one keeps the light colours for tokens it doesn't name.
		var buf bytes.Buffer
		buf.WriteString("@media not all and (prefers-color-scheme: dark) {\n")
		codeFormatter.WriteCSS(&buf, styles.Get("github")) //nolint:errcheck
		buf.WriteString("}\n@media (prefers-color-scheme: dark) {\n")
		codeFormatter.WriteCSS(&buf, styles.Get("github-dark")) //nolint:errcheck
		buf.WriteString("}\n")
		codeCSS = buf.Bytes()
	})
	return codeCSS
}
