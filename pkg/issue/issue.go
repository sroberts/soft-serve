// Package issue is issue tracking for repositories, after Linear: each
// repository has a key (ICE) and numbers its issues (ICE-12); an issue has a
// status on a fixed workflow, a priority, labels, an assignee, a parent,
// comments and an activity log, and is linked to the commits that mention
// it. A pushed commit that says "Fixes ICE-12" on the default branch
// completes it.
//
// This package is the rules and the storage. Who may do what is decided in
// package ops, which every surface (web, REST, MCP, SSH) goes through.
package issue

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// The workflow. Every repository uses the same one.
const (
	Backlog    = "backlog"
	Todo       = "todo"
	InProgress = "in_progress"
	InReview   = "in_review"
	Done       = "done"
	Canceled   = "canceled"
)

// Statuses are the workflow's statuses, in order.
var Statuses = []string{Backlog, Todo, InProgress, InReview, Done, Canceled}

// Categories group statuses the way Linear does.
const (
	CategoryBacklog   = "backlog"
	CategoryUnstarted = "unstarted"
	CategoryStarted   = "started"
	CategoryCompleted = "completed"
	CategoryCanceled  = "canceled"
)

// Category is the group a status belongs to.
func Category(status string) string {
	switch status {
	case Backlog:
		return CategoryBacklog
	case Todo:
		return CategoryUnstarted
	case InProgress, InReview:
		return CategoryStarted
	case Done:
		return CategoryCompleted
	case Canceled:
		return CategoryCanceled
	}
	return ""
}

// IsClosed reports whether an issue with status is finished, one way or
// the other.
func IsClosed(status string) bool { return status == Done || status == Canceled }

// StatusLabel names a status in words.
func StatusLabel(status string) string {
	switch status {
	case Backlog:
		return "Backlog"
	case Todo:
		return "Todo"
	case InProgress:
		return "In Progress"
	case InReview:
		return "In Review"
	case Done:
		return "Done"
	case Canceled:
		return "Canceled"
	}
	return status
}

// ParseStatus reads a status from its name or its label, loosely:
// "In progress", "in-progress" and "in_progress" are all the same.
func ParseStatus(s string) (string, bool) {
	n := strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(s)))
	switch n {
	case "cancelled":
		n = Canceled
	case "todo", "to_do":
		n = Todo
	}
	for _, st := range Statuses {
		if st == n {
			return st, true
		}
	}
	return "", false
}

// Priorities, numbered as Linear numbers them: 0 is none, then urgent
// first.
const (
	PriorityNone   = 0
	PriorityUrgent = 1
	PriorityHigh   = 2
	PriorityMedium = 3
	PriorityLow    = 4
)

// PriorityNames are the priorities' names, by number.
var PriorityNames = []string{"none", "urgent", "high", "medium", "low"}

// PriorityName names a priority.
func PriorityName(p int) string {
	if p < 0 || p >= len(PriorityNames) {
		return PriorityNames[0]
	}
	return PriorityNames[p]
}

// PriorityLabel names a priority in words.
func PriorityLabel(p int) string {
	switch p {
	case PriorityUrgent:
		return "Urgent"
	case PriorityHigh:
		return "High"
	case PriorityMedium:
		return "Medium"
	case PriorityLow:
		return "Low"
	}
	return "No priority"
}

// ParsePriority reads a priority from its name or its number.
func ParsePriority(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "no priority" || s == "no_priority" {
		return PriorityNone, true
	}
	for i, n := range PriorityNames {
		if n == s {
			return i, true
		}
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n < len(PriorityNames) {
		return n, true
	}
	return 0, false
}

// PriorityRank orders priorities for sorting: urgent first, none last.
func PriorityRank(p int) int {
	if p == PriorityNone {
		return len(PriorityNames)
	}
	return p
}

var keyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// ValidKey reports whether key can be a repository's issue key: 2 to 10
// capital letters and digits, starting with a letter.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// DeriveKey suggests an issue key for a repository: the initials of its
// last path segment's words (soft-serve: SS), or the first three letters of
// a single word (icecream: ICE).
func DeriveKey(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	words := strings.FieldsFunc(strings.ToUpper(repo), func(r rune) bool {
		return r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	var key string
	switch {
	case len(words) == 0:
		key = "ISS"
	case len(words) == 1:
		key = words[0]
		if len(key) > 3 {
			key = key[:3]
		}
	default:
		for _, w := range words {
			if len(key) == 4 {
				break
			}
			key += w[:1]
		}
	}
	if key[0] < 'A' || key[0] > 'Z' {
		key = "R" + key
	}
	for len(key) < 2 {
		key += "X"
	}
	if len(key) > 10 {
		key = key[:10]
	}
	return key
}

// Ref is a mention of an issue in a commit message or a branch name.
type Ref struct {
	Key    string
	Number int64
	// Closes is set when a closing word (fixes, closes, resolves) comes
	// before it.
	Closes bool
}

// ID is the issue's identifier, such as ICE-12.
func (r Ref) ID() string { return FormatID(r.Key, r.Number) }

// FormatID is an issue's identifier.
func FormatID(key string, number int64) string { return key + "-" + strconv.FormatInt(number, 10) }

// ParseID reads an identifier such as ICE-12, ignoring case.
func ParseID(s string) (key string, number int64, ok bool) {
	k, n, found := strings.Cut(strings.TrimSpace(s), "-")
	if !found {
		return "", 0, false
	}
	k = strings.ToUpper(k)
	num, err := strconv.ParseInt(n, 10, 64)
	if err != nil || num < 1 || !ValidKey(k) {
		return "", 0, false
	}
	return k, num, true
}

var (
	// An identifier in a message: capitals only, so "utf-8" isn't one.
	messageRef = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9})-([1-9][0-9]{0,9})\b`)
	// A closing word and the list of identifiers after it: "Fixes ICE-1,
	// ICE-2 and ICE-3".
	closing = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b:?\s+((?:[A-Z][A-Z0-9]{1,9}-[1-9][0-9]{0,9}(?:\s*,\s*|\s+and\s+|,?\s+&\s+)?)+)`)
	// An identifier in a branch name, in any case: sroberts/ice-12-fix.
	branchRef = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])([a-z][a-z0-9]{1,9})-([1-9][0-9]{0,9})(?:$|[^0-9])`)
)

// ParseMessage finds the issues a commit message mentions, each once,
// noting which it closes.
func ParseMessage(msg string) []Ref {
	closes := map[string]bool{}
	for _, m := range closing.FindAllStringSubmatch(msg, -1) {
		for _, r := range messageRef.FindAllStringSubmatch(m[1], -1) {
			closes[r[1]+"-"+r[2]] = true
		}
	}
	var refs []Ref
	seen := map[string]bool{}
	for _, m := range messageRef.FindAllStringSubmatch(msg, -1) {
		id := m[1] + "-" + m[2]
		if seen[id] {
			continue
		}
		seen[id] = true
		n, _ := strconv.ParseInt(m[2], 10, 64)
		refs = append(refs, Ref{Key: m[1], Number: n, Closes: closes[id]})
	}
	return refs
}

// ParseBranch finds the issues a branch name mentions, such as
// sroberts/ice-12-fix-the-thing.
func ParseBranch(name string) []Ref {
	var refs []Ref
	seen := map[string]bool{}
	for _, m := range branchRef.FindAllStringSubmatch(name, -1) {
		key := strings.ToUpper(m[1])
		n, _ := strconv.ParseInt(m[2], 10, 64)
		id := FormatID(key, n)
		if seen[id] || !ValidKey(key) {
			continue
		}
		seen[id] = true
		refs = append(refs, Ref{Key: key, Number: n})
	}
	return refs
}
