// Package account is soft's info, pubkey, token and set-username: the SSH
// account commands, run from your own machine against a server's REST API.
package account

import (
	"github.com/charmbracelet/soft-serve/cmd/soft/remote"
	sshcmd "github.com/charmbracelet/soft-serve/pkg/ssh/cmd"
	"github.com/spf13/cobra"
)

// Commands returns the account commands. They are the same as over SSH,
// acting on the account the access token belongs to.
func Commands() []*cobra.Command {
	cmds := sshcmd.AccountCommands()
	for _, c := range cmds {
		c.Short += " on a Soft Serve server"
		c.Long = c.Short + `.

This is the server's "` + c.Name() + `" SSH command, run here over the REST API.
Point it at the server with --remote or $` + remote.RemoteEnv + `, and
authenticate with an access token in $` + remote.TokenEnv + `.`
		remote.Attach(c)
	}
	return cmds
}
