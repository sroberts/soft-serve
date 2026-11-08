package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFitPage(t *testing.T) {
	cases := map[string]string{
		`<h1 id="a">A</h1>`:                   `<h2 id="a">A</h2>`,
		`<h2>B</h2><h5>E</h5>`:                `<h3>B</h3><h6>E</h6>`,
		`<h6>F</h6>`:                          `<h6>F</h6>`,
		`<img src="x.png" alt="x">`:           `<img loading="lazy" src="x.png" alt="x">`,
		`<p>h1 and <hr> stay as they are</p>`: `<p>h1 and <hr> stay as they are</p>`,
	}
	for in, want := range cases {
		if got := string(fitPage([]byte(in))); got != want {
			t.Errorf("fitPage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderMarkdownHeadings(t *testing.T) {
	out := string(renderMarkdown("# Title\n\n## Part\n\n<h1>Raw</h1>\n\n![i](i.png)\n", "", func(p string, _ bool) string { return p }))
	for _, want := range []string{`<h2 id="title">Title</h2>`, `<h3 id="part">Part</h3>`, `<h2>Raw</h2>`, `<img loading="lazy" src="i.png"`} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered Markdown has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<h1") {
		t.Errorf("rendered Markdown still has an h1:\n%s", out)
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                      false,
		"gzip":                  true,
		"br, gzip, deflate":     true,
		"GZIP;q=0.5":            true,
		"gzip;q=0":              false,
		"gzip; q=0.0, identity": false,
		"deflate":               false,
		"x-gzip":                false,
	}
	for hdr, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", hdr)
		if got := acceptsGzip(r); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", hdr, got, want)
		}
	}
}

func TestWriteBodyGzip(t *testing.T) {
	big := bytes.Repeat([]byte("<tr><td>line</td></tr>\n"), 200)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	writeBody(w, r, http.StatusTeapot, big)
	if w.Code != http.StatusTeapot {
		t.Fatalf("status %d, want %d", w.Code, http.StatusTeapot)
	}
	if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers %v", w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("gunzipped body differs (err %v)", err)
	}

	// Small bodies, and clients that don't take gzip, get it plain.
	for _, tc := range []struct {
		enc  string
		body []byte
	}{{"gzip", []byte("small")}, {"", big}} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", tc.enc)
		w := httptest.NewRecorder()
		writeBody(w, r, http.StatusOK, tc.body)
		if w.Header().Get("Content-Encoding") != "" || !bytes.Equal(w.Body.Bytes(), tc.body) {
			t.Errorf("Accept-Encoding %q, %d bytes: want it plain", tc.enc, len(tc.body))
		}
	}
}

// An error page used to write its status before its headers, so it went
// out without its Content-Type or Content-Security-Policy.
func TestErrorPageHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/nope", nil)
	w := httptest.NewRecorder()
	uiHandler{}.renderError(w, r, &page{SiteName: "Soft Serve"}, http.StatusNotFound, "Not here.")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type %q", ct)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("Content-Security-Policy %q", csp)
	}
	if !strings.Contains(w.Body.String(), "Not here.") {
		t.Errorf("body has no message:\n%s", w.Body.String())
	}
}
