// Package remote points soft's client commands at a server: the --remote
// and --token flags, and their environment variables.
package remote

import (
	"errors"
	"os"

	"github.com/charmbracelet/soft-serve/pkg/client"
	sshcmd "github.com/charmbracelet/soft-serve/pkg/ssh/cmd"
	"github.com/spf13/cobra"
)

const (
	// RemoteEnv is the server's HTTP URL.
	RemoteEnv = "SOFT_SERVE_REMOTE"
	// TokenEnv is the access token to act with.
	TokenEnv = "SOFT_SERVE_TOKEN"
)

// Attach makes cmd, one of the SSH command trees, act through the REST API
// of the server named by --remote or $SOFT_SERVE_REMOTE, with the access
// token in --token or $SOFT_SERVE_TOKEN.
func Attach(cmd *cobra.Command) {
	var remote, token string
	cmd.PersistentFlags().StringVar(&remote, "remote", "", "the server's HTTP URL (default $"+RemoteEnv+")")
	cmd.PersistentFlags().StringVar(&token, "token", "", "access token (default $"+TokenEnv+"; prefer the variable, flags show up in ps)")

	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		if remote == "" {
			remote = os.Getenv(RemoteEnv)
		}
		if remote == "" {
			return errors.New("set the server with --remote or $" + RemoteEnv)
		}
		if token == "" {
			token = os.Getenv(TokenEnv)
		}
		cl, err := client.New(remote, token)
		if err != nil {
			return err
		}
		c.SetContext(sshcmd.WithService(c.Context(), cl))
		return nil
	}

	// The commands print results with cobra's Println, which defaults to
	// stderr; over SSH the session sets stdout, and so does this.
	cmd.SetOut(os.Stdout)
}
