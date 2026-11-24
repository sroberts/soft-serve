package migrate

import (
	"context"

	"github.com/charmbracelet/soft-serve/pkg/db"
)

const (
	issuesName    = "issues"
	issuesVersion = 4
)

// issues adds issue tracking: a tracker (key and numbering) per repository,
// issues, labels, comments, an activity log, and links to commits.
var issues = Migration{
	Name:    issuesName,
	Version: issuesVersion,
	Migrate: func(ctx context.Context, tx *db.Tx) error {
		return migrateUp(ctx, tx, issuesVersion, issuesName)
	},
	Rollback: func(ctx context.Context, tx *db.Tx) error {
		return migrateDown(ctx, tx, issuesVersion, issuesName)
	},
}
