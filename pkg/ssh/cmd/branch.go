package cmd

import (
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/spf13/cobra"
)

func branchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "branch",
		Short: "Manage repository branches",
	}

	cmd.AddCommand(
		branchListCommand(),
		branchDefaultCommand(),
		branchDeleteCommand(),
	)

	return cmd
}

func branchListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list REPOSITORY",
		Short: "List repository branches",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := service(cmd.Context()).Repo(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, b := range r.Branches {
				cmd.Println(b)
			}
			return nil
		},
	}
}

func branchDefaultCommand() *cobra.Command {
	return repoSetting{
		use:   "default REPOSITORY [BRANCH]",
		short: "Set or get the default branch",
		args:  cobra.RangeArgs(1, 2),
		get: func(cmd *cobra.Command, r ops.Repo) error {
			if r.DefaultBranch == "" {
				return ops.Invalid("repository has no default branch yet")
			}
			cmd.Println(r.DefaultBranch)
			return nil
		},
		set: func(v []string) (ops.RepoUpdate, error) {
			return ops.RepoUpdate{DefaultBranch: &v[0]}, nil
		},
	}.command()
}

func branchDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "delete REPOSITORY BRANCH",
		Aliases: []string{"remove", "rm", "del"},
		Short:   "Delete a branch",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return service(cmd.Context()).DeleteBranch(cmd.Context(), args[0], args[1])
		},
	}
}
