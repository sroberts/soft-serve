package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/soft-serve/pkg/issue"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/spf13/cobra"
)

// issueService is what the issue commands act through. The soft client's
// REST service doesn't offer issues yet.
func issueService(ctx context.Context) (ops.IssueService, error) {
	if s, ok := service(ctx).(ops.IssueService); ok {
		return s, nil
	}
	return nil, errors.New("issues aren't available through this client yet; use ssh")
}

// issueArgs reads an issue given as an identifier (ICE-12) or as a
// repository and a number, and returns the arguments left over.
func issueArgs(ctx context.Context, svc ops.IssueService, args []string) (string, int64, []string, error) {
	if len(args) == 0 {
		return "", 0, nil, errors.New("give an issue, such as ICE-12, or a repository and a number")
	}
	if _, _, ok := issue.ParseID(args[0]); ok {
		repo, n, err := svc.FindIssue(ctx, args[0])
		return repo, n, args[1:], err
	}
	if len(args) < 2 {
		return "", 0, nil, fmt.Errorf("%q isn't an issue identifier such as ICE-12; give a repository and a number instead", args[0])
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(args[1], "#"), 10, 64)
	if err != nil || n < 1 {
		return "", 0, nil, fmt.Errorf("%q isn't an issue number", args[1])
	}
	return args[0], n, args[2:], nil
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func issueCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "issue",
		Aliases: []string{"issues"},
		Short:   "Track issues",
		Long: `Track a repository's issues.

An issue is named by its identifier, such as ICE-12 (the repository's key and
its number), or by the repository and the number: "ice 12".

Statuses: ` + strings.Join(issue.Statuses, ", ") + `.
Priorities: ` + strings.Join(issue.PriorityNames, ", ") + `.

A pushed commit that mentions ICE-12 is linked to it; one on the default
branch that says "Fixes ICE-12" (or closes, resolves) moves it to done. A new
branch named for an issue, such as you/ice-12-thing, moves it to in progress.`,
	}
	cmd.AddCommand(
		issueListCommand(),
		issueViewCommand(),
		issueCreateCommand(),
		issueEditCommand(),
		issueStatusCommand("close", "Move an issue to done", issue.Done),
		issueStatusCommand("cancel", "Move an issue to canceled", issue.Canceled),
		issueStatusCommand("reopen", "Move an issue back to todo", issue.Todo),
		issueCommentCommand(),
		issueDeleteCommand(),
		issueKeyCommand(),
		issueLabelCommand(),
		issueProjectCommand(),
		issueCycleCommand(),
	)
	return cmd
}

func printIssues(w io.Writer, is []ops.Issue, showRepo bool) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, i := range is {
		labels := ""
		if len(i.Labels) > 0 {
			labels = "[" + strings.Join(i.Labels, ", ") + "]"
		}
		repo := ""
		if showRepo {
			repo = i.Repo + "\t"
		}
		fmt.Fprintf(tw, "%s\t%s%s\t%s\t%s\t%s %s\n", i.ID, repo, issue.StatusLabel(i.Status), issue.PriorityLabel(priorityNumber(i.Priority)), i.Assignee, i.Title, labels) //nolint:errcheck
	}
	tw.Flush() //nolint:errcheck
}

func priorityNumber(name string) int {
	n, _ := issue.ParsePriority(name)
	return n
}

func issueListCommand() *cobra.Command {
	var q ops.IssueQuery
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list [REPOSITORY]",
		Short: "List open issues",
		Long:  "List a repository's open issues, or with no repository, those of every repository you can read.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				q.Repo = args[0]
			}
			l, err := svc.ListIssues(cmd.Context(), q)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), l)
			}
			printIssues(cmd.OutOrStdout(), l.Issues, q.Repo == "")
			if l.Total > len(l.Issues) {
				cmd.Printf("… %d of %d shown\n", len(l.Issues), l.Total)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVarP(&q.Status, "status", "s", nil, "only these statuses")
	cmd.Flags().BoolVarP(&q.Closed, "all", "a", false, "include done and canceled issues")
	cmd.Flags().StringSliceVarP(&q.Priority, "priority", "p", nil, "only these priorities")
	cmd.Flags().StringVar(&q.Assignee, "assignee", "", `only issues assigned to this user, "me" or "none"`)
	cmd.Flags().StringSliceVarP(&q.Label, "label", "l", nil, "only issues with all these labels")
	cmd.Flags().StringVar(&q.Project, "project", "", `only issues in this project, or "none"`)
	cmd.Flags().StringVar(&q.Cycle, "cycle", "", `only issues in this cycle (a number, current, next, previous), or "none"`)
	cmd.Flags().StringVarP(&q.Query, "query", "q", "", "only issues whose title or description contains this")
	cmd.Flags().StringVar(&q.Sort, "sort", "", "priority (the default), updated, created or number")
	cmd.Flags().IntVarP(&q.Limit, "limit", "n", 50, "how many to show, up to 500")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func issueViewCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "view ISSUE",
		Short: "Show an issue, its comments and activity",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			repo, n, _, err := issueArgs(cmd.Context(), svc, args)
			if err != nil {
				return err
			}
			d, err := svc.Issue(cmd.Context(), repo, n)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), d)
			}
			cmd.Printf("%s  %s\n", d.ID, d.Title)
			cmd.Printf("Status:    %s\nPriority:  %s\n", issue.StatusLabel(d.Status), issue.PriorityLabel(priorityNumber(d.Priority)))
			if d.Assignee != "" {
				cmd.Printf("Assignee:  %s\n", d.Assignee)
			}
			if len(d.Labels) > 0 {
				cmd.Printf("Labels:    %s\n", strings.Join(d.Labels, ", "))
			}
			if d.Parent != "" {
				cmd.Printf("Parent:    %s\n", d.Parent)
			}
			if d.Project != "" {
				cmd.Printf("Project:   %s\n", d.Project)
			}
			if d.Cycle != 0 {
				cmd.Printf("Cycle:     %d\n", d.Cycle)
			}
			if d.Creator != "" {
				cmd.Printf("Created:   %s by %s\n", d.CreatedAt.Format("2 Jan 2006 15:04"), d.Creator)
			}
			if d.Description != "" {
				cmd.Printf("\n%s\n", d.Description)
			}
			if len(d.SubIssues) > 0 {
				cmd.Println("\nSub-issues:")
				printIssues(cmd.OutOrStdout(), d.SubIssues, false)
			}
			if len(d.Commits) > 0 {
				cmd.Println("\nCommits:")
				for _, c := range d.Commits {
					fixes := ""
					if c.Closes {
						fixes = " (fixes)"
					}
					cmd.Printf("  %s %s%s\n", c.SHA[:min(7, len(c.SHA))], c.Summary, fixes)
				}
			}
			for _, c := range d.Comments {
				cmd.Printf("\n— %s, %s (#%d)\n%s\n", c.Author, c.CreatedAt.Format("2 Jan 2006 15:04"), c.ID, c.Body)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func issueCreateCommand() *cobra.Command {
	var c ops.IssueCreate
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "create REPOSITORY TITLE...",
		Short: "Create an issue",
		Long:  "Create an issue. The words after the repository are its title; over ssh they needn't be quoted.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			c.Title = strings.Join(args[1:], " ")
			i, err := svc.CreateIssue(cmd.Context(), args[0], c)
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), i)
			}
			cmd.Println(i.ID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&c.Description, "description", "d", "", "what it's about, in Markdown")
	cmd.Flags().StringVarP(&c.Status, "status", "s", "", "status; todo when left out")
	cmd.Flags().StringVarP(&c.Priority, "priority", "p", "", "priority")
	cmd.Flags().StringVar(&c.Assignee, "assignee", "", `who it's assigned to, or "me"`)
	cmd.Flags().StringSliceVarP(&c.Labels, "label", "l", nil, "labels, created if new")
	cmd.Flags().StringVar(&c.Parent, "parent", "", "the issue it's part of")
	cmd.Flags().StringVar(&c.Project, "project", "", "the project it's in")
	cmd.Flags().StringVar(&c.Cycle, "cycle", "", "the cycle it's in: a number, current or next")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func issueEditCommand() *cobra.Command {
	var title, desc, status, priority, assignee, parent, project, cycle, after string
	var add, remove []string
	cmd := &cobra.Command{
		Use:   "edit ISSUE",
		Short: "Change an issue",
		Long:  `Change an issue's fields. Set --assignee or --parent to "" to clear them.`,
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			repo, n, _, err := issueArgs(cmd.Context(), svc, args)
			if err != nil {
				return err
			}
			u := ops.IssueUpdate{AddLabels: add, RemoveLabels: remove}
			set := func(flag string, v string) *string {
				if cmd.Flags().Changed(flag) {
					return &v
				}
				return nil
			}
			u.Title, u.Description, u.Status = set("title", title), set("description", desc), set("status", status)
			u.Priority, u.Assignee, u.Parent = set("priority", priority), set("assignee", assignee), set("parent", parent)
			u.Project, u.Cycle, u.After = set("project", project), set("cycle", cycle), set("after", after)
			i, err := svc.UpdateIssue(cmd.Context(), repo, n, u)
			if err != nil {
				return err
			}
			cmd.Printf("%s  %s  %s\n", i.ID, issue.StatusLabel(i.Status), i.Title)
			return nil
		},
	}
	cmd.Flags().StringVarP(&title, "title", "t", "", "title")
	cmd.Flags().StringVarP(&desc, "description", "d", "", "description, in Markdown")
	cmd.Flags().StringVarP(&status, "status", "s", "", "status")
	cmd.Flags().StringVarP(&priority, "priority", "p", "", "priority")
	cmd.Flags().StringVar(&assignee, "assignee", "", `who it's assigned to, "me", or "" for nobody`)
	cmd.Flags().StringVar(&parent, "parent", "", `the issue it's part of, or "" for none`)
	cmd.Flags().StringVar(&project, "project", "", `the project it's in, or "" for none`)
	cmd.Flags().StringVar(&cycle, "cycle", "", `the cycle it's in (a number, current, next), or "" for none`)
	cmd.Flags().StringVar(&after, "after", "", `its place on the board: "top", or the issue it goes after`)
	cmd.Flags().StringSliceVar(&add, "add-label", nil, "labels to add, created if new")
	cmd.Flags().StringSliceVar(&remove, "remove-label", nil, "labels to take off")
	return cmd
}

func issueStatusCommand(use, short, status string) *cobra.Command {
	return &cobra.Command{
		Use:   use + " ISSUE",
		Short: short,
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			repo, n, _, err := issueArgs(cmd.Context(), svc, args)
			if err != nil {
				return err
			}
			i, err := svc.UpdateIssue(cmd.Context(), repo, n, ops.IssueUpdate{Status: &status})
			if err != nil {
				return err
			}
			cmd.Printf("%s  %s\n", i.ID, issue.StatusLabel(i.Status))
			return nil
		},
	}
}

func issueCommentCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "comment ISSUE TEXT...",
		Short: "Comment on an issue",
		Long:  "Comment on an issue, given as ICE-12 or as a repository and a number. The words after it are the comment.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			repo, n, rest, err := issueArgs(cmd.Context(), svc, args)
			if err != nil {
				return err
			}
			if len(rest) == 0 {
				return errors.New("give the comment's text after the issue")
			}
			c, err := svc.AddIssueComment(cmd.Context(), repo, n, strings.Join(rest, " "))
			if err != nil {
				return err
			}
			cmd.Printf("Comment #%d added.\n", c.ID)
			return nil
		},
	}
}

func issueDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete ISSUE",
		Short: "Delete an issue for good",
		Long:  "Delete an issue, its comments and activity, for good. Canceling it keeps the record.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			repo, n, _, err := issueArgs(cmd.Context(), svc, args)
			if err != nil {
				return err
			}
			return svc.DeleteIssue(cmd.Context(), repo, n)
		},
	}
}

func issueKeyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "key REPOSITORY [KEY]",
		Short: "Show or change a repository's issue key",
		Long:  "Show a repository's issue key, such as ICE in ICE-12, or change it. Changing it renames every issue: ICE-12 becomes NEW-12.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 2 {
				return svc.SetIssueKey(cmd.Context(), args[0], args[1])
			}
			t, err := svc.IssueTracker(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			cmd.Println(t.Key)
			return nil
		},
	}
}

func issueLabelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "label",
		Aliases: []string{"labels"},
		Short:   "Manage a repository's issue labels",
	}
	var color, desc string
	create := &cobra.Command{
		Use:   "create REPOSITORY NAME",
		Short: "Add a label",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			_, err = svc.CreateIssueLabel(cmd.Context(), args[0], ops.IssueLabel{Name: args[1], Color: color, Description: desc})
			return err
		},
	}
	create.Flags().StringVarP(&color, "color", "c", "", "hex color, such as #e5484d")
	create.Flags().StringVarP(&desc, "description", "d", "", "what it's for")
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list REPOSITORY",
			Short: "List labels",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				svc, err := issueService(cmd.Context())
				if err != nil {
					return err
				}
				ls, err := svc.IssueLabels(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				for _, l := range ls {
					fmt.Fprintf(tw, "%s\t%s\t%s\n", l.Name, l.Color, l.Description) //nolint:errcheck
				}
				return tw.Flush()
			},
		},
		create,
		&cobra.Command{
			Use:   "delete REPOSITORY NAME",
			Short: "Delete a label, taking it off every issue",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				svc, err := issueService(cmd.Context())
				if err != nil {
					return err
				}
				return svc.DeleteIssueLabel(cmd.Context(), args[0], args[1])
			},
		},
	)
	return cmd
}

func progressText(p ops.IssueProgress) string {
	return fmt.Sprintf("%d of %d done (%d%%)", p.Completed, p.Total, p.Percent)
}

func issueProjectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project",
		Aliases: []string{"projects"},
		Short:   "Manage a repository's projects",
		Long:    "A project is a body of work: issues put in it count toward its progress. Statuses: " + strings.Join(issue.ProjectStatuses, ", ") + ". Dates are written 2026-12-15.",
	}
	list := &cobra.Command{
		Use:   "list REPOSITORY",
		Short: "List projects and their progress",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			ps, err := svc.IssueProjects(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			for _, p := range ps {
				target := ""
				if p.TargetDate != "" {
					target = "due " + p.TargetDate
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, issue.ProjectStatusLabel(p.Status), progressText(p.Progress), target, p.Lead) //nolint:errcheck
			}
			return tw.Flush()
		},
	}
	var in ops.IssueProjectInput
	create := &cobra.Command{
		Use:   "create REPOSITORY NAME...",
		Short: "Add a project",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			in.Name = strings.Join(args[1:], " ")
			p, err := svc.CreateIssueProject(cmd.Context(), args[0], in)
			if err != nil {
				return err
			}
			cmd.Printf("Project %q added.\n", p.Name)
			return nil
		},
	}
	create.Flags().StringVarP(&in.Description, "description", "d", "", "what it's for")
	create.Flags().StringVarP(&in.Status, "status", "s", "", "status; planned when left out")
	create.Flags().StringVar(&in.Lead, "lead", "", `who leads it, or "me"`)
	create.Flags().StringVar(&in.StartDate, "start", "", "start date, such as 2026-12-01")
	create.Flags().StringVar(&in.TargetDate, "target", "", "target date, such as 2026-12-15")

	var name, desc, status, lead, start, target string
	edit := &cobra.Command{
		Use:   "edit REPOSITORY PROJECT...",
		Short: "Change a project",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			set := func(flag, v string) *string {
				if cmd.Flags().Changed(flag) {
					return &v
				}
				return nil
			}
			p, err := svc.UpdateIssueProject(cmd.Context(), args[0], strings.Join(args[1:], " "), ops.IssueProjectUpdate{
				Name: set("name", name), Description: set("description", desc), Status: set("status", status),
				Lead: set("lead", lead), StartDate: set("start", start), TargetDate: set("target", target),
			})
			if err != nil {
				return err
			}
			cmd.Printf("%s  %s  %s\n", p.Name, issue.ProjectStatusLabel(p.Status), progressText(p.Progress))
			return nil
		},
	}
	edit.Flags().StringVarP(&name, "name", "n", "", "new name")
	edit.Flags().StringVarP(&desc, "description", "d", "", "description")
	edit.Flags().StringVarP(&status, "status", "s", "", "status")
	edit.Flags().StringVar(&lead, "lead", "", `lead, "me", or "" for none`)
	edit.Flags().StringVar(&start, "start", "", `start date, or "" for none`)
	edit.Flags().StringVar(&target, "target", "", `target date, or "" for none`)

	del := &cobra.Command{
		Use:   "delete REPOSITORY PROJECT...",
		Short: "Delete a project; its issues stay, in no project",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			return svc.DeleteIssueProject(cmd.Context(), args[0], strings.Join(args[1:], " "))
		},
	}
	cmd.AddCommand(list, create, edit, del)
	return cmd
}

func issueCycleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cycle",
		Aliases: []string{"cycles"},
		Short:   "Manage a repository's cycles",
		Long:    "A cycle is a numbered, time-boxed sprint. Name one by its number, or as current, next or previous. Dates are written 2026-12-15.",
	}
	list := &cobra.Command{
		Use:   "list REPOSITORY",
		Short: "List cycles, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			cs, err := svc.IssueCycles(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			for _, c := range cs {
				cur := ""
				if c.Current {
					cur = "current"
				}
				fmt.Fprintf(tw, "Cycle %d\t%s\t%s to %s\t%s\t%s\n", c.Number, c.Name, c.StartDate, c.EndDate, progressText(c.Progress), cur) //nolint:errcheck
			}
			return tw.Flush()
		},
	}
	var in ops.IssueCycleInput
	create := &cobra.Command{
		Use:   "create REPOSITORY",
		Short: "Add the next cycle",
		Long:  "Add the next cycle. Without dates it starts the day after the last one ends, or today, and lasts two weeks.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			c, err := svc.CreateIssueCycle(cmd.Context(), args[0], in)
			if err != nil {
				return err
			}
			cmd.Printf("Cycle %d: %s to %s\n", c.Number, c.StartDate, c.EndDate)
			return nil
		},
	}
	create.Flags().StringVarP(&in.Name, "name", "n", "", "a name")
	create.Flags().StringVar(&in.StartDate, "start", "", "start date")
	create.Flags().StringVar(&in.EndDate, "end", "", "end date")

	var name, start, end string
	edit := &cobra.Command{
		Use:   "edit REPOSITORY CYCLE",
		Short: "Change a cycle's name or dates",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			set := func(flag, v string) *string {
				if cmd.Flags().Changed(flag) {
					return &v
				}
				return nil
			}
			c, err := svc.UpdateIssueCycle(cmd.Context(), args[0], args[1], ops.IssueCycleUpdate{Name: set("name", name), StartDate: set("start", start), EndDate: set("end", end)})
			if err != nil {
				return err
			}
			cmd.Printf("Cycle %d: %s to %s\n", c.Number, c.StartDate, c.EndDate)
			return nil
		},
	}
	edit.Flags().StringVarP(&name, "name", "n", "", "name")
	edit.Flags().StringVar(&start, "start", "", "start date")
	edit.Flags().StringVar(&end, "end", "", "end date")

	del := &cobra.Command{
		Use:   "delete REPOSITORY CYCLE",
		Short: "Delete a cycle; its issues stay, in no cycle",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := issueService(cmd.Context())
			if err != nil {
				return err
			}
			return svc.DeleteIssueCycle(cmd.Context(), args[0], args[1])
		},
	}
	cmd.AddCommand(list, create, edit, del)
	return cmd
}
