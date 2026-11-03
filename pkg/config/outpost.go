package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

// Outpost (https://github.com/sroberts/outpost) supervises Soft Serve as a
// native process. It reaches every service on loopback and puts a Tailscale
// node in front of it, so under Outpost nothing should bind a public
// interface. See outpost-service-spec.md.
const (
	// outpostStorageEnv is set by Outpost for every service it runs, and is
	// also the working directory, so the default relative data path already
	// lands inside it.
	outpostStorageEnv = "OUTPOST_STORAGE_DIR"

	// outpostPortEnv carries the HTTP port Outpost probes and proxies to.
	outpostPortEnv = "OUTPOST_PORT"
)

// underOutpost reports whether Outpost started this process.
func underOutpost() bool {
	return os.Getenv(outpostStorageEnv) != ""
}

// loopback rebinds a listen address such as ":23231" to 127.0.0.1, keeping
// its port. An address that already names a host is left alone.
func loopback(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "" {
		return addr
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// applyOutpostDefaults binds every listener to loopback when Outpost runs
// Soft Serve. These are defaults: they are written to a new config.yaml like
// any other, and the file or a SOFT_SERVE_* variable can still change them.
func applyOutpostDefaults(c *Config) {
	if !underOutpost() {
		return
	}
	c.SSH.ListenAddr = loopback(c.SSH.ListenAddr)
	c.Git.ListenAddr = loopback(c.Git.ListenAddr)
	c.HTTP.ListenAddr = loopback(c.HTTP.ListenAddr)
	c.Stats.ListenAddr = loopback(c.Stats.ListenAddr)
}

// applyOutpostPort points the HTTP server at 127.0.0.1:$OUTPOST_PORT. It runs
// after the config file, so a port Outpost reassigns is picked up without
// editing config.yaml, and before the SOFT_SERVE_* variables, so an explicit
// SOFT_SERVE_HTTP_LISTEN_ADDR still wins.
func applyOutpostPort(c *Config) error {
	v := os.Getenv(outpostPortEnv)
	if v == "" {
		return nil
	}
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s: %q is not a port number", outpostPortEnv, v)
	}
	c.HTTP.ListenAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return nil
}
