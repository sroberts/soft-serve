package web

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Running git directly, for what the git module has no call for: archives,
// blame, grep, for-each-ref and a streamed log. Every revision handed to git
// has been resolved to a hash or checked against revPattern first, and every
// path comes after "--", so nothing a caller sends is read as an option.

// gitEnv keeps the user's and system's git configuration, prompts and pagers
// out of what the server runs.
var gitEnv = append(os.Environ(),
	"GIT_CONFIG_GLOBAL=/dev/null",
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_TERMINAL_PROMPT=0",
	"GIT_PAGER=cat",
)

// gitError is a failed git command, with the start of what it said.
type gitError struct {
	args   []string
	err    error
	stderr string
	code   int
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", e.args[0], e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

// exitCode is git's exit status for err, or -1.
func exitCode(err error) int {
	var ge *gitError
	if errors.As(err, &ge) {
		return ge.code
	}
	return -1
}

func gitCommand(ctx context.Context, dir string, args ...string) (*exec.Cmd, *limitWriter) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv
	stderr := &limitWriter{n: 1024}
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	return cmd, stderr
}

func wrapGitErr(args []string, err error, stderr *limitWriter) error {
	if err == nil {
		return nil
	}
	ge := &gitError{args: args, err: err, stderr: strings.TrimSpace(string(stderr.buf)), code: -1}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		ge.code = ee.ExitCode()
	}
	return ge
}

// runGit runs git in dir, writing its output to stdout.
func runGit(ctx context.Context, dir string, stdout io.Writer, args ...string) error {
	cmd, stderr := gitCommand(ctx, dir, args...)
	cmd.Stdout = stdout
	return wrapGitErr(args, cmd.Run(), stderr)
}

// gitOutput runs git in dir and returns its output.
func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := runGit(ctx, dir, &out, args...)
	return out.Bytes(), err
}

// errStopScan ends a scanGit early without it being an error.
var errStopScan = errors.New("stop")

// scanGit runs git in dir and hands each line of its output to line, which
// can stop it early by returning errStopScan. Stopping kills git.
func scanGit(ctx context.Context, dir string, line func(string) error, args ...string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, stderr := gitCommand(ctx, dir, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return wrapGitErr(args, err, stderr)
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var stop error
	for sc.Scan() {
		if err := line(sc.Text()); err != nil {
			stop = err
			break
		}
	}
	if stop != nil {
		cancel()
		_ = cmd.Wait()
		if errors.Is(stop, errStopScan) {
			return nil
		}
		return stop
	}
	if err := sc.Err(); err != nil {
		cancel()
		_ = cmd.Wait()
		return err
	}
	return wrapGitErr(args, cmd.Wait(), stderr)
}

// CommitBrief is a commit in a line: what a file list or a blame shows.
type CommitBrief struct {
	SHA      string    `json:"sha"`
	ShortSHA string    `json:"short_sha"`
	Summary  string    `json:"summary"`
	Author   string    `json:"author"`
	When     time.Time `json:"when"`
}

func newBrief(sha, unix, author, summary string) CommitBrief {
	var when time.Time
	var secs int64
	if _, err := fmt.Sscan(unix, &secs); err == nil {
		when = time.Unix(secs, 0).UTC()
	}
	return CommitBrief{SHA: sha, ShortSHA: sha[:min(7, len(sha))], Summary: summary, Author: author, When: when}
}
