// Package repo is `soft repo`: the SSH repo commands, run from your own
// machine against a server's REST API.
package repo

import (
	"errors"
	"os"

	"github.com/charmbracelet/soft-serve/pkg/client"
	sshcmd "github.com/charmbracelet/soft-serve/pkg/ssh/cmd"
	"github.com/spf13/cobra"
)

const (
	remoteEnv = "SOFT_SERVE_REMOTE"
	tokenEnv  = "SOFT_SERVE_TOKEN"
)

// Command returns `soft repo`. It is the same command tree as `repo` over
// SSH, so the commands, flags and output match, acting through the REST API
// with an access token instead of an SSH key.
func Command() *cobra.Command {
	var remote, token string

	cmd := sshcmd.ServiceRepoCommand()
	cmd.Short = "Manage repositories on a Soft Serve server"
	cmd.Long = `Manage repositories on a Soft Serve server over its REST API.

These are the server's "repo" SSH commands, run from here. Point them at
the server with --remote or $` + remoteEnv + `, and authenticate with an
access token in $` + tokenEnv + ` (create one with "ssh <server> token
create <name>"). Without a token you act as an anonymous user.`
	cmd.Example = `  export ` + remoteEnv + `=https://git.example.com
  export ` + tokenEnv + `=ss_...
  soft repo import --mirror --auto-sync=false soft-serve https://github.com/charmbracelet/soft-serve
  soft repo sync soft-serve`

	cmd.PersistentFlags().StringVar(&remote, "remote", "", "the server's HTTP URL (default $"+remoteEnv+")")
	cmd.PersistentFlags().StringVar(&token, "token", "", "access token (default $"+tokenEnv+"; prefer the variable, flags show up in ps)")

	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		if remote == "" {
			remote = os.Getenv(remoteEnv)
		}
		if remote == "" {
			return errors.New("set the server with --remote or $" + remoteEnv)
		}
		if token == "" {
			token = os.Getenv(tokenEnv)
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
	return cmd
}
