package config

import (
	"testing"

	"github.com/matryer/is"
)

func TestLoopback(t *testing.T) {
	is := is.New(t)
	is.Equal(loopback(":23231"), "127.0.0.1:23231")
	is.Equal(loopback("0.0.0.0:23231"), "0.0.0.0:23231") // named a host on purpose
	is.Equal(loopback("localhost:23233"), "localhost:23233")
	is.Equal(loopback("not an address"), "not an address")
}

func TestDefaultsBindAllInterfacesOutsideOutpost(t *testing.T) {
	is := is.New(t)
	t.Setenv(outpostStorageEnv, "")
	cfg := DefaultConfig()
	is.Equal(cfg.SSH.ListenAddr, ":23231")
	is.Equal(cfg.Git.ListenAddr, ":9418")
	is.Equal(cfg.HTTP.ListenAddr, ":23232")
}

func TestDefaultsBindLoopbackUnderOutpost(t *testing.T) {
	is := is.New(t)
	t.Setenv(outpostStorageEnv, t.TempDir())
	cfg := DefaultConfig()
	is.Equal(cfg.SSH.ListenAddr, "127.0.0.1:23231")
	is.Equal(cfg.Git.ListenAddr, "127.0.0.1:9418")
	is.Equal(cfg.HTTP.ListenAddr, "127.0.0.1:23232")
	is.Equal(cfg.Stats.ListenAddr, "localhost:23233")
}

// The loopback defaults are written to a new config.yaml, so they still hold
// on the next start, when the file is read.
func TestOutpostDefaultsSurviveTheConfigFile(t *testing.T) {
	is := is.New(t)
	t.Setenv(outpostStorageEnv, t.TempDir())
	t.Setenv("SOFT_SERVE_DATA_PATH", t.TempDir())
	is.NoErr(DefaultConfig().WriteConfig())

	t.Setenv(outpostStorageEnv, "") // the file alone must carry them
	cfg := DefaultConfig()
	is.NoErr(cfg.Parse())
	is.Equal(cfg.SSH.ListenAddr, "127.0.0.1:23231")
	is.Equal(cfg.HTTP.ListenAddr, "127.0.0.1:23232")
}

func TestOutpostPortSetsTheHTTPListener(t *testing.T) {
	is := is.New(t)
	t.Setenv("SOFT_SERVE_DATA_PATH", t.TempDir())
	t.Setenv(outpostPortEnv, "8123")
	cfg := DefaultConfig()
	is.NoErr(cfg.ParseEnv())
	is.Equal(cfg.HTTP.ListenAddr, "127.0.0.1:8123")
}

// Outpost may hand out a different port later; the one in config.yaml must
// not hide it.
func TestOutpostPortOverridesTheConfigFile(t *testing.T) {
	is := is.New(t)
	t.Setenv("SOFT_SERVE_DATA_PATH", t.TempDir())
	is.NoErr(DefaultConfig().WriteConfig())

	t.Setenv(outpostPortEnv, "8123")
	cfg := DefaultConfig()
	is.NoErr(cfg.Parse())
	is.Equal(cfg.HTTP.ListenAddr, "127.0.0.1:8123")
}

func TestExplicitListenAddrBeatsOutpostPort(t *testing.T) {
	is := is.New(t)
	t.Setenv("SOFT_SERVE_DATA_PATH", t.TempDir())
	t.Setenv(outpostPortEnv, "8123")
	t.Setenv("SOFT_SERVE_HTTP_LISTEN_ADDR", "127.0.0.1:9999")
	cfg := DefaultConfig()
	is.NoErr(cfg.ParseEnv())
	is.Equal(cfg.HTTP.ListenAddr, "127.0.0.1:9999")
}

func TestOutpostPortMustBeAPort(t *testing.T) {
	for _, v := range []string{"abc", "0", "65536", "-1", "80x"} {
		t.Run(v, func(t *testing.T) {
			is := is.New(t)
			t.Setenv("SOFT_SERVE_DATA_PATH", t.TempDir())
			t.Setenv(outpostPortEnv, v)
			is.True(DefaultConfig().ParseEnv() != nil)
		})
	}
}
