package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The issue tools. Unlike the rest, some of them change things: they act
// as the caller, under the same rules as the web pages, the REST API and
// SSH (ops.IssueService).

const issueToolsGuide = `
Issues: every repository has issues, named by identifier, such as ICE-12 (the
repository's key and a number). The workflow is backlog, todo, in_progress,
in_review, done, canceled; priorities are urgent, high, medium, low or none.
list_issues with no repo searches every repository you can read; assignee
"me" lists your own. Creating, changing and commenting need write access to
the repository. A pushed commit that says "Fixes ICE-12" on the default
branch moves the issue to done by itself. Issues can belong to a project (a
named body of work with a status, lead, dates and progress) and a cycle (a
numbered, time-boxed sprint; "current" and "next" name them by date).`

func writeTool(title string, destructive bool) *mcp.ToolAnnotations {
	f := false
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: &destructive, IdempotentHint: false, OpenWorldHint: &f}
}

// opsTool adapts an ops call to an MCP tool handler, reporting the
// caller's mistakes as they are and anything else as an internal error.
func opsTool[In, Out any](f func(context.Context, In) (Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := f(ctx, in)
		if err != nil {
			var zero Out
			switch opsStatus(err, false) {
			case http.StatusInternalServerError:
				return nil, zero, errInternal
			case http.StatusForbidden:
				if errors.Is(err, proto.ErrUnauthorized) {
					return nil, zero, mcpError("you don't have the access this needs: reading a repository's issues needs read access, and changing them write access")
				}
			}
			return nil, zero, err
		}
		return nil, out, nil
	}
}

// issueRef names an issue by identifier, or by repository and number.
type issueRef struct {
	ID     string `json:"id,omitempty" jsonschema:"the issue's identifier, such as ICE-12; or give repo and number instead"`
	Repo   string `json:"repo,omitempty" jsonschema:"repository name, with number, when id is not given"`
	Number int64  `json:"number,omitempty" jsonschema:"the issue's number in repo"`
}

func (r issueRef) resolve(ctx context.Context, svc ops.IssueService) (string, int64, error) {
	if r.ID != "" {
		return svc.FindIssue(ctx, r.ID)
	}
	if r.Repo == "" || r.Number < 1 {
		return "", 0, ops.Invalid("give the issue's id, such as ICE-12, or its repo and number")
	}
	return r.Repo, r.Number, nil
}

type listIssuesArgs struct {
	Repo               string   `json:"repo,omitempty" jsonschema:"repository name; every repository you can read when omitted"`
	Status             []string `json:"status,omitempty" jsonschema:"only these statuses: backlog, todo, in_progress, in_review, done, canceled"`
	IncludeClosed      bool     `json:"include_closed,omitempty" jsonschema:"when no status is given, include done and canceled issues too; only open ones are listed otherwise"`
	Priority           []string `json:"priority,omitempty" jsonschema:"only these priorities: urgent, high, medium, low, none"`
	Assignee           string   `json:"assignee,omitempty" jsonschema:"only issues assigned to this username; \"me\" for your own, \"none\" for unassigned"`
	Label              []string `json:"label,omitempty" jsonschema:"only issues with all of these labels"`
	Parent             string   `json:"parent,omitempty" jsonschema:"only the sub-issues of this issue (an identifier such as ICE-12)"`
	Project            string   `json:"project,omitempty" jsonschema:"only issues in this project, by name; \"none\" for issues in none. Needs repo"`
	Cycle              string   `json:"cycle,omitempty" jsonschema:"only issues in this cycle: its number, current, next or previous; \"none\" for issues in none. Needs repo"`
	Query              string   `json:"query,omitempty" jsonschema:"only issues whose title or description contains this, ignoring case, or whose identifier it is"`
	Sort               string   `json:"sort,omitempty" jsonschema:"priority (urgent first, the default), updated, created, number or manual"`
	Limit              int      `json:"limit,omitempty" jsonschema:"how many, up to 500; 50 when omitted"`
	Offset             int      `json:"offset,omitempty" jsonschema:"how many to skip, for the next page"`
	IncludeDescription bool     `json:"include_description,omitempty" jsonschema:"include each issue's description"`
}

type createIssueArgs struct {
	Repo        string   `json:"repo" jsonschema:"repository name"`
	Title       string   `json:"title" jsonschema:"a short title, up to 256 characters"`
	Description string   `json:"description,omitempty" jsonschema:"what it's about, in Markdown, up to 64 KiB"`
	Status      string   `json:"status,omitempty" jsonschema:"backlog, todo (the default), in_progress, in_review, done or canceled"`
	Priority    string   `json:"priority,omitempty" jsonschema:"urgent, high, medium, low or none (the default)"`
	Assignee    string   `json:"assignee,omitempty" jsonschema:"a username who can read the repository, or \"me\""`
	Labels      []string `json:"labels,omitempty" jsonschema:"label names; ones that don't exist yet are created"`
	Parent      string   `json:"parent,omitempty" jsonschema:"the issue this is a sub-issue of, in the same repository (ICE-12 or its number)"`
	Project     string   `json:"project,omitempty" jsonschema:"a project's name, as list_issue_projects gives it"`
	Cycle       string   `json:"cycle,omitempty" jsonschema:"a cycle: its number, current or next"`
}

type updateIssueArgs struct {
	issueRef
	Title        *string   `json:"title,omitempty" jsonschema:"new title"`
	Description  *string   `json:"description,omitempty" jsonschema:"new description, in Markdown; replaces the old one"`
	Status       *string   `json:"status,omitempty" jsonschema:"backlog, todo, in_progress, in_review, done or canceled"`
	Priority     *string   `json:"priority,omitempty" jsonschema:"urgent, high, medium, low or none"`
	Assignee     *string   `json:"assignee,omitempty" jsonschema:"a username, \"me\", or \"\" to unassign"`
	Parent       *string   `json:"parent,omitempty" jsonschema:"the parent issue, or \"\" for none"`
	Project      *string   `json:"project,omitempty" jsonschema:"a project's name, or \"\" to take it out of its project"`
	Cycle        *string   `json:"cycle,omitempty" jsonschema:"a cycle: its number, current or next; or \"\" to take it out of its cycle"`
	Labels       *[]string `json:"labels,omitempty" jsonschema:"the full set of labels, replacing the current ones"`
	AddLabels    []string  `json:"add_labels,omitempty" jsonschema:"labels to add; created if new"`
	RemoveLabels []string  `json:"remove_labels,omitempty" jsonschema:"labels to take off"`
	After        *string   `json:"after,omitempty" jsonschema:"its place in its status column's manual order (the board's): \"top\", or the identifier of the issue it goes right after"`
}

type commentArgs struct {
	issueRef
	Body string `json:"body" jsonschema:"the comment, in Markdown"`
}

type editCommentArgs struct {
	issueRef
	CommentID int64  `json:"comment_id" jsonschema:"the comment's id, from get_issue"`
	Body      string `json:"body" jsonschema:"the new text, in Markdown"`
}

type labelArgs struct {
	Repo        string `json:"repo" jsonschema:"repository name"`
	Name        string `json:"name" jsonschema:"the label's name, up to 50 characters"`
	Color       string `json:"color,omitempty" jsonschema:"a hex color such as #e5484d"`
	Description string `json:"description,omitempty" jsonschema:"what the label is for"`
}

type labelList struct {
	Labels []ops.IssueLabel `json:"labels"`
}

type projectArgs struct {
	Repo        string `json:"repo" jsonschema:"repository name"`
	Name        string `json:"name" jsonschema:"the project's name, up to 80 characters"`
	Description string `json:"description,omitempty" jsonschema:"what it's for, in Markdown"`
	Status      string `json:"status,omitempty" jsonschema:"planned (the default), started, paused, completed or canceled"`
	Lead        string `json:"lead,omitempty" jsonschema:"a username who can read the repository, or \"me\""`
	StartDate   string `json:"start_date,omitempty" jsonschema:"written 2026-12-15"`
	TargetDate  string `json:"target_date,omitempty" jsonschema:"when it should be done, written 2026-12-15"`
}

type updateProjectArgs struct {
	Repo        string  `json:"repo" jsonschema:"repository name"`
	Project     string  `json:"project" jsonschema:"the project's current name"`
	Name        *string `json:"name,omitempty" jsonschema:"a new name"`
	Description *string `json:"description,omitempty" jsonschema:"a new description, in Markdown"`
	Status      *string `json:"status,omitempty" jsonschema:"planned, started, paused, completed or canceled"`
	Lead        *string `json:"lead,omitempty" jsonschema:"a username, \"me\", or \"\" for none"`
	StartDate   *string `json:"start_date,omitempty" jsonschema:"2026-12-15, or \"\" for none"`
	TargetDate  *string `json:"target_date,omitempty" jsonschema:"2026-12-15, or \"\" for none"`
}

type cycleArgs struct {
	Repo      string `json:"repo" jsonschema:"repository name"`
	Name      string `json:"name,omitempty" jsonschema:"an optional name; cycles are numbered either way"`
	StartDate string `json:"start_date,omitempty" jsonschema:"written 2026-12-15; the day after the last cycle ends (or today) when omitted"`
	EndDate   string `json:"end_date,omitempty" jsonschema:"written 2026-12-15; two weeks after it starts when omitted"`
}

type updateCycleArgs struct {
	Repo      string  `json:"repo" jsonschema:"repository name"`
	Cycle     string  `json:"cycle" jsonschema:"the cycle: its number, current, next or previous"`
	Name      *string `json:"name,omitempty" jsonschema:"a new name"`
	StartDate *string `json:"start_date,omitempty" jsonschema:"2026-12-15"`
	EndDate   *string `json:"end_date,omitempty" jsonschema:"2026-12-15"`
}

type projectList struct {
	Projects []ops.IssueProject `json:"projects"`
}

type cycleList struct {
	Cycles []ops.IssueCycle `json:"cycles"`
}

type deleted struct {
	Deleted string `json:"deleted"`
}

func addIssueTools(s *mcp.Server, b *browser) {
	svc := b.service()

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_issues",
		Description: "List issues, most urgent first: in one repository, or with no repo, every repository you can read. " +
			"Only open issues unless you give statuses or include_closed. Returns the page and the total that match.",
		Annotations: readOnly("List issues"),
	}, opsTool(func(ctx context.Context, in listIssuesArgs) (ops.IssueList, error) {
		return svc.ListIssues(ctx, ops.IssueQuery{
			Repo: in.Repo, Status: in.Status, Closed: in.IncludeClosed, Priority: in.Priority, Assignee: in.Assignee,
			Label: in.Label, Parent: in.Parent, Project: in.Project, Cycle: in.Cycle, Query: in.Query, Sort: in.Sort, Limit: in.Limit, Offset: in.Offset,
			Description: in.IncludeDescription,
		})
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_issue",
		Description: "Get one issue in full: description, sub-issues, comments (with their ids), activity, and the commits that mention it.",
		Annotations: readOnly("Get issue"),
	}, opsTool(func(ctx context.Context, in issueRef) (ops.IssueDetail, error) {
		repo, n, err := in.resolve(ctx, svc)
		if err != nil {
			return ops.IssueDetail{}, err
		}
		return svc.Issue(ctx, repo, n)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_issue_tracker",
		Description: "Describe a repository's issue tracking: its key (as in ICE-12), the workflow's statuses, the priorities, its labels, and how many issues are open and closed.",
		Annotations: readOnly("Get issue tracker"),
	}, opsTool(func(ctx context.Context, in repoArg) (ops.IssueTracker, error) {
		return svc.IssueTracker(ctx, in.Repo)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_issue",
		Description: "Create an issue in a repository. Returns it with its identifier, such as ICE-12. Needs write access.",
		Annotations: writeTool("Create issue", false),
	}, opsTool(func(ctx context.Context, in createIssueArgs) (ops.Issue, error) {
		return svc.CreateIssue(ctx, in.Repo, ops.IssueCreate{
			Title: in.Title, Description: in.Description, Status: in.Status, Priority: in.Priority,
			Assignee: in.Assignee, Labels: in.Labels, Parent: in.Parent, Project: in.Project, Cycle: in.Cycle,
		})
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "update_issue",
		Description: "Change an issue: only the fields you give change, and each change is recorded in its activity. " +
			"Use status to move it along the workflow (in_progress, done, canceled…). Needs write access.",
		Annotations: writeTool("Update issue", false),
	}, opsTool(func(ctx context.Context, in updateIssueArgs) (ops.Issue, error) {
		repo, n, err := in.resolve(ctx, svc)
		if err != nil {
			return ops.Issue{}, err
		}
		return svc.UpdateIssue(ctx, repo, n, ops.IssueUpdate{
			Title: in.Title, Description: in.Description, Status: in.Status, Priority: in.Priority,
			Assignee: in.Assignee, Parent: in.Parent, Project: in.Project, Cycle: in.Cycle,
			Labels: in.Labels, AddLabels: in.AddLabels, RemoveLabels: in.RemoveLabels, After: in.After,
		})
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "comment_on_issue",
		Description: "Add a comment to an issue, in Markdown. Needs write access.",
		Annotations: writeTool("Comment on issue", false),
	}, opsTool(func(ctx context.Context, in commentArgs) (ops.IssueComment, error) {
		repo, n, err := in.resolve(ctx, svc)
		if err != nil {
			return ops.IssueComment{}, err
		}
		return svc.AddIssueComment(ctx, repo, n, in.Body)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "edit_issue_comment",
		Description: "Replace a comment's text. Only its author or a repository admin can.",
		Annotations: writeTool("Edit comment", false),
	}, opsTool(func(ctx context.Context, in editCommentArgs) (ops.IssueComment, error) {
		repo, n, err := in.resolve(ctx, svc)
		if err != nil {
			return ops.IssueComment{}, err
		}
		return svc.UpdateIssueComment(ctx, repo, n, in.CommentID, in.Body)
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_issue_labels",
		Description: "List a repository's issue labels, with their colors and descriptions.",
		Annotations: readOnly("List labels"),
	}, opsTool(func(ctx context.Context, in repoArg) (labelList, error) {
		ls, err := svc.IssueLabels(ctx, in.Repo)
		return labelList{Labels: ls}, err
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_issue_label",
		Description: "Add a label to a repository, with an optional color and description. (create_issue and update_issue also create labels they're given that don't exist.) Needs write access.",
		Annotations: writeTool("Create label", false),
	}, opsTool(func(ctx context.Context, in labelArgs) (ops.IssueLabel, error) {
		return svc.CreateIssueLabel(ctx, in.Repo, ops.IssueLabel{Name: in.Name, Color: in.Color, Description: in.Description})
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_issue_projects",
		Description: "List a repository's projects, started ones first: status, lead, dates, and progress (issues completed of the total, canceled left out).",
		Annotations: readOnly("List projects"),
	}, opsTool(func(ctx context.Context, in repoArg) (projectList, error) {
		ps, err := svc.IssueProjects(ctx, in.Repo)
		return projectList{Projects: ps}, err
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_issue_project",
		Description: "Add a project to a repository. Put issues in it with create_issue or update_issue. Needs write access.",
		Annotations: writeTool("Create project", false),
	}, opsTool(func(ctx context.Context, in projectArgs) (ops.IssueProject, error) {
		return svc.CreateIssueProject(ctx, in.Repo, ops.IssueProjectInput{
			Name: in.Name, Description: in.Description, Status: in.Status, Lead: in.Lead, StartDate: in.StartDate, TargetDate: in.TargetDate,
		})
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_issue_project",
		Description: "Change a project: only the fields you give change. Needs write access.",
		Annotations: writeTool("Update project", false),
	}, opsTool(func(ctx context.Context, in updateProjectArgs) (ops.IssueProject, error) {
		return svc.UpdateIssueProject(ctx, in.Repo, in.Project, ops.IssueProjectUpdate{
			Name: in.Name, Description: in.Description, Status: in.Status, Lead: in.Lead, StartDate: in.StartDate, TargetDate: in.TargetDate,
		})
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_issue_cycles",
		Description: "List a repository's cycles, newest first, with their dates, whether each is the current one, and progress.",
		Annotations: readOnly("List cycles"),
	}, opsTool(func(ctx context.Context, in repoArg) (cycleList, error) {
		cs, err := svc.IssueCycles(ctx, in.Repo)
		return cycleList{Cycles: cs}, err
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_issue_cycle",
		Description: "Add the next cycle to a repository. Without dates it starts the day after the last one ends (or today) and lasts two weeks. Cycles can't overlap. Needs write access.",
		Annotations: writeTool("Create cycle", false),
	}, opsTool(func(ctx context.Context, in cycleArgs) (ops.IssueCycle, error) {
		return svc.CreateIssueCycle(ctx, in.Repo, ops.IssueCycleInput{Name: in.Name, StartDate: in.StartDate, EndDate: in.EndDate})
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_issue_cycle",
		Description: "Change a cycle's name or dates. Needs write access.",
		Annotations: writeTool("Update cycle", false),
	}, opsTool(func(ctx context.Context, in updateCycleArgs) (ops.IssueCycle, error) {
		return svc.UpdateIssueCycle(ctx, in.Repo, in.Cycle, ops.IssueCycleUpdate{Name: in.Name, StartDate: in.StartDate, EndDate: in.EndDate})
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "delete_issue",
		Description: "Delete an issue for good, with its comments and activity; its sub-issues lose their parent. " +
			"This can't be undone: prefer update_issue with status canceled unless asked to delete. Needs write access.",
		Annotations: writeTool("Delete issue", true),
	}, opsTool(func(ctx context.Context, in issueRef) (deleted, error) {
		repo, n, err := in.resolve(ctx, svc)
		if err != nil {
			return deleted{}, err
		}
		d, err := svc.Issue(ctx, repo, n)
		if err != nil {
			return deleted{}, err
		}
		return deleted{Deleted: d.ID}, svc.DeleteIssue(ctx, repo, n)
	}))
}

// issueToolNames are the tools addIssueTools registers, for the
// instructions and tests.
var issueToolNames = strings.Fields("list_issues get_issue get_issue_tracker create_issue update_issue comment_on_issue edit_issue_comment list_issue_labels create_issue_label delete_issue list_issue_projects create_issue_project update_issue_project list_issue_cycles create_issue_cycle update_issue_cycle")
