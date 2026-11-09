package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// RepoCommand returns the repository commands an SSH session gets.
func RepoCommand() *cobra.Command {
	cmd := repoRoot()
	cmd.AddCommand(serviceRepoCommands()...)
	// These read the repository on the server directly.
	cmd.AddCommand(
		blobCommand(),
		commitCommand(),
		treeCommand(),
	)
	return cmd
}

// ServiceRepoCommand returns the repository commands that act only through
// an ops.Service, which the soft client runs against a server's REST API.
// Put the service in the command's context with WithService.
func ServiceRepoCommand() *cobra.Command {
	cmd := repoRoot()
	cmd.AddCommand(serviceRepoCommands()...)
	return cmd
}

func repoRoot() *cobra.Command {
	return &cobra.Command{
		Use:     "repo",
		Aliases: []string{"repos", "repository", "repositories"},
		Short:   "Manage repositories",
	}
}

func serviceRepoCommands() []*cobra.Command {
	return []*cobra.Command{
		autoSyncCommand(),
		branchCommand(),
		collabCommand(),
		createCommand(),
		deleteCommand(),
		descriptionCommand(),
		hiddenCommand(),
		importCommand(),
		infoRepoCommand(),
		listCommand(),
		mirrorCommand(),
		privateCommand(),
		projectName(),
		renameCommand(),
		syncCommand(),
		tagCommand(),
		webhookCommand(),
	}
}

func infoRepoCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "info REPOSITORY",
		Short: "Get information about a repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := service(cmd.Context()).Repo(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			// project name and description are optional, handle trailing
			// whitespace to avoid breaking tests.
			cmd.Println(strings.TrimSpace(fmt.Sprint("Project Name: ", r.ProjectName)))
			cmd.Println("Repository:", r.Name)
			cmd.Println(strings.TrimSpace(fmt.Sprint("Description: ", r.Description)))
			cmd.Println("Private:", r.Private)
			cmd.Println("Hidden:", r.Hidden)
			cmd.Println("Mirror:", r.Mirror)
			if r.AutoSync != nil {
				cmd.Println("Auto Sync:", *r.AutoSync)
			}
			if r.Owner != "" {
				cmd.Println("Owner:", r.Owner)
			}
			cmd.Println(strings.TrimSpace(fmt.Sprint("Default Branch: ", r.DefaultBranch)))
			if len(r.Branches) > 0 {
				cmd.Println("Branches:")
				for _, b := range r.Branches {
					cmd.Println("  -", b)
				}
			}
			if len(r.Tags) > 0 {
				cmd.Println("Tags:")
				for _, t := range r.Tags {
					cmd.Println("  -", t)
				}
			}
			return nil
		},
	}
}
