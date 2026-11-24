package migrate

import (
	"context"

	"github.com/charmbracelet/soft-serve/pkg/db"
)

const (
	issueProjectsName    = "issue_projects"
	issueProjectsVersion = 5
)

// issueProjects adds projects (a named body of work with a status, a lead
// and dates) and cycles (numbered, time-boxed sprints), and lets an issue
// belong to one of each.
var issueProjects = Migration{
	Name:    issueProjectsName,
	Version: issueProjectsVersion,
	Migrate: func(ctx context.Context, tx *db.Tx) error {
		return migrateUp(ctx, tx, issueProjectsVersion, issueProjectsName)
	},
	Rollback: func(ctx context.Context, tx *db.Tx) error {
		return migrateDown(ctx, tx, issueProjectsVersion, issueProjectsName)
	},
}
