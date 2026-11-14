package web

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Recent work: the newest commits across every repository the caller can
// read, on any branch, so the home page, GET /v1/recent and MCP's
// recent_activity pick up where the owner left off.

const (
	defaultRecent = 10
	// homeRecent is how many the home page shows, above the repositories.
	homeRecent = 6
	maxRecent  = 50
	// recentWorkers bounds the git processes run at once for a listing.
	recentWorkers = 4
)

// LatestCommit is a repository's newest branch tip, and its branch.
type LatestCommit struct {
	CommitBrief
	Branch string `json:"branch"`
}

// RecentCommit is a commit in recent work, with where it is.
type RecentCommit struct {
	CommitBrief
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

// Recent is the newest commits across repositories, newest first.
type Recent struct {
	Commits []RecentCommit `json:"commits"`
}

// withRecent reads the newest commits of each of repos, up to limit, in
// one git process per repository and a few at a time. It sets each one's
// LastCommit and returns the newest across them all: a commit on several
// branches is listed once.
func (b *browser) withRecent(ctx context.Context, repos []RepoInfo, limit int) Recent {
	if limit <= 0 {
		limit = defaultRecent
	}
	limit = min(limit, maxRecent)

	var (
		mu  sync.Mutex
		all []RecentCommit
		wg  sync.WaitGroup
		sem = make(chan struct{}, recentWorkers)
	)
	for i := range repos {
		if repos[i].Empty {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			found := b.repoRecent(ctx, repos[i].Name, limit)
			if len(found) > 0 {
				first := found[0]
				repos[i].LastCommit = &LatestCommit{CommitBrief: first.CommitBrief, Branch: first.Branch}
			}
			mu.Lock()
			all = append(all, found...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	slices.SortStableFunc(all, func(x, y RecentCommit) int {
		if c := y.When.Compare(x.When); c != 0 {
			return c
		}
		if c := strings.Compare(x.Repo, y.Repo); c != 0 {
			return c
		}
		return strings.Compare(x.SHA, y.SHA)
	})
	if len(all) > limit {
		all = all[:limit]
	}
	if all == nil {
		all = []RecentCommit{}
	}
	return Recent{Commits: all}
}

// repoRecent is one repository's newest commits across its branches. With
// --source, git names the branch it reached each commit from first, which
// is the most recently updated one holding it.
func (b *browser) repoRecent(ctx context.Context, name string, limit int) []RecentCommit {
	_, gr, err := b.open(ctx, name)
	if err != nil {
		return nil
	}
	var out []RecentCommit
	_ = scanGit(ctx, gr.Path, func(line string) error {
		f := strings.Split(line, "\x1f")
		if len(f) < 5 {
			return nil
		}
		out = append(out, RecentCommit{
			CommitBrief: newBrief(f[0], f[2], f[3], f[4]),
			Repo:        name,
			Branch:      strings.TrimPrefix(f[1], "refs/heads/"),
		})
		return nil
	}, "log", "--branches", "--source", "-n", strconv.Itoa(limit), "--format=%H%x1f%S%x1f%ct%x1f%an%x1f%s")
	return out
}

// sortRecent orders repositories newest first by their latest commit; ones
// with none go last, by name.
func sortRecent(repos []RepoInfo) {
	slices.SortStableFunc(repos, func(x, y RepoInfo) int {
		switch {
		case x.LastCommit == nil && y.LastCommit == nil:
			return strings.Compare(x.Name, y.Name)
		case x.LastCommit == nil:
			return 1
		case y.LastCommit == nil:
			return -1
		}
		if c := y.LastCommit.When.Compare(x.LastCommit.When); c != 0 {
			return c
		}
		return strings.Compare(x.Name, y.Name)
	})
}
