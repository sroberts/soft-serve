// Package repo is `soft repo`: the SSH repo commands, run from your own
// machine against a server's REST API.
package repo

import (
	"github.com/charmbracelet/soft-serve/cmd/soft/remote"
	sshcmd "github.com/charmbracelet/soft-serve/pkg/ssh/cmd"
	"github.com/spf13/cobra"
)

// Command returns `soft repo`. It is the same command tree as `repo` over
// SSH, so the commands, flags and output match, acting through the REST API
// with an access token instead of an SSH key.
func Command() *cobra.Command {
	cmd := sshcmd.ServiceRepoCommand()
	cmd.Short = "Manage repositories on a Soft Serve server"
	cmd.Long = `Manage repositories on a Soft Serve server over its REST API.

These are the server's "repo" SSH commands, run from here. Point them at
the server with --remote or $` + remote.RemoteEnv + `, and authenticate with an
access token in $` + remote.TokenEnv + ` (create one with "ssh <server> token
create <name>"). Without a token you act as an anonymous user.`
	cmd.Example = `  export ` + remote.RemoteEnv + `=https://git.example.com
  export ` + remote.TokenEnv + `=ss_...
  soft repo import --mirror --auto-sync=false soft-serve https://github.com/charmbracelet/soft-serve
  soft repo sync soft-serve`
	remote.Attach(cmd)
	return cmd
}
