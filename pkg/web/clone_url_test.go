package web

import (
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/matryer/is"
)

// The SSH address shown is the configured one unless that points at this
// machine and the caller is somewhere else (#35).
func TestSSHURL(t *testing.T) {
	cases := []struct {
		name, public, listen, host, fwdHost, want string
		disabled                                  bool
	}{
		{name: "configured", public: "ssh://git.example.com", listen: ":23231", host: "soft.example.com", want: "ssh://git.example.com"},
		{name: "default, viewed locally", public: "ssh://localhost:23231", listen: ":23231", host: "localhost:23232", want: "ssh://localhost:23231"},
		{name: "default, viewed on 127.0.0.1", public: "ssh://localhost:23231", listen: ":23231", host: "127.0.0.1:23232", want: "ssh://localhost:23231"},
		{name: "default, viewed remotely", public: "ssh://localhost:23231", listen: ":23231", host: "soft.example.com:23232", want: "ssh://soft.example.com:23231"},
		{name: "forwarded host", public: "ssh://localhost:23231", listen: "0.0.0.0:23231", host: "127.0.0.1:8080", fwdHost: "soft.example.com", want: "ssh://soft.example.com:23231"},
		{name: "port 22 omitted", public: "ssh://127.0.0.1", listen: ":22", host: "soft.example.com", want: "ssh://soft.example.com"},
		{name: "ipv6 viewer", public: "ssh://localhost:23231", listen: ":23231", host: "[2001:db8::1]:23232", want: "ssh://[2001:db8::1]:23231"},
		{name: "loopback listener (Outpost)", public: "ssh://localhost:23231", listen: "127.0.0.1:23231", host: "soft.example.ts.net", want: ""},
		{name: "unset", public: "", listen: ":23231", host: "soft.example.com", want: ""},
		{name: "disabled", public: "ssh://git.example.com", listen: ":23231", host: "soft.example.com", disabled: true, want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			is := is.New(t)
			cfg := config.DefaultConfig()
			cfg.SSH.Enabled = !c.disabled
			cfg.SSH.PublicURL = c.public
			cfg.SSH.ListenAddr = c.listen
			r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
			r.Host = c.host
			if c.fwdHost != "" {
				r.Header.Set("X-Forwarded-Host", c.fwdHost)
			}
			is.Equal(sshURL(cfg, r), c.want)
		})
	}
	is := is.New(t)
	is.Equal(sshURL(nil, httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)), "")
	is.Equal(sshCommand("ssh://soft.example.com:23231"), "ssh -p 23231 soft.example.com")
	is.Equal(sshCommand("ssh://soft.example.com"), "ssh soft.example.com")
	is.Equal(sshCommand(""), "")
}

// The clone panel follows the address the page was opened at, and when no
// SSH address would work it says so to admins only.
func TestClonePanelSSH(t *testing.T) {
	f := newViewFixture(t)
	cfg := config.FromContext(f.ctx)
	cfg.SSH.Enabled = true
	cfg.SSH.PublicURL = "ssh://localhost:23231"
	cfg.SSH.ListenAddr = ":23231"

	w := f.do(req{path: "/group/open"})
	has(t, w, "git clone ssh://"+viewHost+":23231/group/open")
	hasNot(t, w, "localhost:23231", "SOFT_SERVE_SSH_PUBLIC_URL")

	cfg.SSH.ListenAddr = "127.0.0.1:23231"
	hasNot(t, f.do(req{path: "/group/open"}), "git clone ssh://", "SOFT_SERVE_SSH_PUBLIC_URL")
	w = f.do(req{path: "/group/open", auth: f.adminAuth(t)})
	hasNot(t, w, "git clone ssh://")
	has(t, w, "SOFT_SERVE_SSH_PUBLIC_URL")

	cfg.SSH.PublicURL = "ssh://git.example.com"
	w = f.do(req{path: "/group/open", auth: f.adminAuth(t)})
	has(t, w, "git clone ssh://git.example.com/group/open")
	hasNot(t, w, "SOFT_SERVE_SSH_PUBLIC_URL")
}
