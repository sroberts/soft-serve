package cmd

import (
	"context"
	"testing"

	"charm.land/ssh"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/charmbracelet/soft-serve/pkg/sshutils"
	"github.com/matryer/is"
)

type mockSession struct {
	ssh.Session
	env []string
}

func (m mockSession) Environ() []string { return m.env }

func TestBuildGitEnv(t *testing.T) {
	is := is.New(t)
	cfg := config.DefaultConfig()
	cfg.DataPath = t.TempDir()

	mockSess := mockSession{
		env: []string{"LD_PRELOAD=malicious.so", "FOO=bar", "GIT_PROTOCOL=2"},
	}
	ctx := context.WithValue(context.Background(), sshutils.ContextKeySession, mockSess)

	envs := buildGitEnv(ctx, cfg, nil, "testrepo", "/tmp/data/repos", "testrepo.git", "ssh-ed25519 AAAAC3...")

	hasLDPreload := false
	hasFoo := false
	hasGitProtocol := false
	for _, e := range envs {
		if e == "LD_PRELOAD=malicious.so" {
			hasLDPreload = true
		}
		if e == "FOO=bar" {
			hasFoo = true
		}
		if e == "GIT_PROTOCOL=2" {
			hasGitProtocol = true
		}
	}

	is.True(!hasLDPreload)  // Should NOT contain LD_PRELOAD
	is.True(!hasFoo)        // Should NOT contain FOO=bar
	is.True(hasGitProtocol) // Should contain GIT_PROTOCOL
}
