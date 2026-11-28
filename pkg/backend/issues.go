package backend

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/soft-serve/git"
	"github.com/charmbracelet/soft-serve/pkg/access"
	"github.com/charmbracelet/soft-serve/pkg/db"
	"github.com/charmbracelet/soft-serve/pkg/hooks"
	"github.com/charmbracelet/soft-serve/pkg/issue"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// maxLinkedCommits bounds how many of a push's commits are read for issue
// mentions.
const maxLinkedCommits = 200

// linkIssues connects a push to the issues it mentions, as Linear's Git
// integration does:
//
//   - every pushed commit that mentions an issue (ICE-12) is linked to it;
//   - one that says "Fixes ICE-12" (or closes, resolves) on the default
//     branch moves it to done;
//   - a new branch named for an issue (sroberts/ice-12-thing) moves it from
//     backlog or todo to in progress.
//
// Only issues in repositories the pusher can write to are touched.
func (d *Backend) linkIssues(ctx context.Context, user proto.User, r proto.Repository, arg hooks.HookArg) {
	branch, ok := strings.CutPrefix(arg.RefName, "refs/heads/")
	if !ok || git.IsZeroHash(arg.NewSha) || user == nil {
		return
	}
	gr, err := r.Open()
	if err != nil {
		d.logger.Error("issues: opening repository", "repo", r.Name(), "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	defaultBranch := ""
	if out, err := gitCmd(ctx, gr.Path, "symbolic-ref", "-q", "HEAD"); err == nil {
		defaultBranch = strings.TrimPrefix(strings.TrimSpace(out), "refs/heads/")
	}
	onDefault := branch == defaultBranch

	l := issueLinker{d: d, ctx: ctx, user: user, repo: r, h: db.FromContext(ctx)}

	if git.IsZeroHash(arg.OldSha) && !onDefault {
		for _, ref := range issue.ParseBranch(branch) {
			l.branch(ref, branch)
		}
	}

	revs := []string{arg.NewSha}
	if git.IsZeroHash(arg.OldSha) {
		// A new branch: only the commits no other branch has yet. The
		// update hook runs before the ref exists, so --branches leaves it
		// out by itself.
		revs = append(revs, "--not", "--branches")
	} else {
		revs = []string{arg.OldSha + ".." + arg.NewSha}
	}
	args := append([]string{"log", "-z", "--format=%H%x1f%B", "-n", strconv.Itoa(maxLinkedCommits)}, revs...)
	args = append(args, "--")
	out, err := gitCmd(ctx, gr.Path, args...)
	if err != nil {
		d.logger.Error("issues: reading pushed commits", "repo", r.Name(), "err", err)
		return
	}
	for _, rec := range strings.Split(out, "\x00") {
		sha, msg, ok := strings.Cut(rec, "\x1f")
		if !ok || len(sha) < 7 {
			continue
		}
		summary, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
		for _, ref := range issue.ParseMessage(msg) {
			ref.Closes = ref.Closes && onDefault
			l.commit(ref, strings.TrimSpace(sha), summary)
		}
	}
}

type issueLinker struct {
	d    *Backend
	ctx  context.Context
	user proto.User
	repo proto.Repository
	h    *db.DB
	// writable caches whether the pusher can write to a repository.
	writable map[int64]bool
}

// find is the issue a reference names, if the pusher can change it.
func (l *issueLinker) find(ref issue.Ref) (issue.Row, bool) {
	t, err := issue.TrackerByKey(l.ctx, l.h, ref.Key)
	if err != nil {
		return issue.Row{}, false
	}
	row, err := issue.ByNumber(l.ctx, l.h, t.RepoID, ref.Number)
	if err != nil {
		return issue.Row{}, false
	}
	if l.writable == nil {
		l.writable = map[int64]bool{}
	}
	ok, seen := l.writable[row.RepoID]
	if !seen {
		ok = l.user.IsAdmin() || l.d.AccessLevelForUser(l.ctx, row.RepoName, l.user) >= access.ReadWriteAccess
		l.writable[row.RepoID] = ok
	}
	return row, ok
}

func (l *issueLinker) actor() sql.NullInt64 {
	return sql.NullInt64{Int64: l.user.ID(), Valid: true}
}

// commit links a commit to the issue it mentions, completing it if the
// commit closes it.
func (l *issueLinker) commit(ref issue.Ref, sha, summary string) {
	row, ok := l.find(ref)
	if !ok {
		return
	}
	err := l.h.TransactionContext(l.ctx, func(tx *db.Tx) error {
		added, err := issue.LinkCommit(l.ctx, tx, issue.Commit{IssueID: row.ID, RepoID: l.repo.ID(), SHA: sha, Summary: summary, Closes: ref.Closes})
		if err != nil {
			return err
		}
		if added {
			if err := issue.Log(l.ctx, tx, row.ID, l.actor(), issue.EventCommit, l.repo.Name(), sha); err != nil {
				return err
			}
		}
		if ref.Closes && !issue.IsClosed(row.Status) {
			return l.setStatus(tx, row, issue.Done)
		}
		if added {
			return issue.Touch(l.ctx, tx, row.ID)
		}
		return nil
	})
	if err != nil {
		l.d.logger.Error("issues: linking a commit", "issue", ref.ID(), "sha", sha, "err", err)
	}
}

// branch starts an issue when a branch named for it is pushed.
func (l *issueLinker) branch(ref issue.Ref, name string) {
	row, ok := l.find(ref)
	if !ok {
		return
	}
	err := l.h.TransactionContext(l.ctx, func(tx *db.Tx) error {
		if err := issue.Log(l.ctx, tx, row.ID, l.actor(), issue.EventBranch, l.repo.Name(), name); err != nil {
			return err
		}
		switch issue.Category(row.Status) {
		case issue.CategoryBacklog, issue.CategoryUnstarted:
			return l.setStatus(tx, row, issue.InProgress)
		}
		return issue.Touch(l.ctx, tx, row.ID)
	})
	if err != nil {
		l.d.logger.Error("issues: starting from a branch", "issue", ref.ID(), "branch", name, "err", err)
	}
}

func (l *issueLinker) setStatus(tx *db.Tx, row issue.Row, status string) error {
	from := row.Status
	row.Status = status
	if err := issue.Update(l.ctx, tx, row, true); err != nil {
		return err
	}
	return issue.Log(l.ctx, tx, row.ID, l.actor(), issue.EventStatus, from, status)
}

// gitCmd runs git in a repository and returns what it prints.
func gitCmd(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	return out.String(), nil
}
