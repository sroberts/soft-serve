package proto

import (
	"errors"
)

var (
	// ErrUnauthorized is returned when the user is not authorized to perform action.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrInvalidRemote is returned when a repository import remote is invalid.
	ErrInvalidRemote = errors.New("remote must be a network URL")
	// ErrImportFailed is returned when cloning an import remote fails. It
	// carries no detail on purpose: the git error would tell the importing
	// user whether a host is up, which port answered, and what it said.
	ErrImportFailed = errors.New("import failed")
	// ErrImportsDisabled is returned when the server has turned imports off,
	// which it does when the installed git is too old to enforce the remote
	// validation. The reason stays in the server log.
	ErrImportsDisabled = errors.New("imports are disabled on this server")
	// ErrNotMirror is returned when a mirror-only action is asked of a
	// repository that is not a mirror.
	ErrNotMirror = errors.New("repository is not a mirror")
	// ErrMirrorSyncFailed is returned when syncing a mirror from its
	// upstream fails. Like ErrImportFailed it carries no detail, so a sync
	// cannot be used to probe hosts and ports; the server log has it.
	ErrMirrorSyncFailed = errors.New("mirror sync failed")
	// ErrMirrorSyncInProgress is returned when a mirror is already syncing.
	ErrMirrorSyncInProgress = errors.New("mirror sync already in progress")
	// ErrFileNotFound is returned when the file is not found.
	ErrFileNotFound = errors.New("file not found")
	// ErrRepoNotFound is returned when a repository is not found.
	ErrRepoNotFound = errors.New("repository not found")
	// ErrRepoExist is returned when a repository already exists.
	ErrRepoExist = errors.New("repository already exists")
	// ErrUserNotFound is returned when a user is not found.
	ErrUserNotFound = errors.New("user not found")
	// ErrUserExist is returned when a username is already taken.
	ErrUserExist = errors.New("username is already taken")
	// ErrPublicKeyExist is returned when adding a key the account already has.
	ErrPublicKeyExist = errors.New("you already have this public key")
	// ErrPublicKeyInUse is returned when adding a key another account has.
	ErrPublicKeyInUse = errors.New("public key belongs to another account")
	// ErrPublicKeyNotFound is returned when removing a key the account
	// doesn't have.
	ErrPublicKeyNotFound = errors.New("public key not found")
	// ErrUserOwnsRepos is returned when deleting a user who still owns
	// repositories.
	ErrUserOwnsRepos = errors.New("user still owns repositories; delete them first")
	// ErrTokenNotFound is returned when a token is not found.
	ErrTokenNotFound = errors.New("token not found")
	// ErrTokenExpired is returned when a token is expired.
	ErrTokenExpired = errors.New("token expired")
	// ErrCollaboratorNotFound is returned when a collaborator is not found.
	ErrCollaboratorNotFound = errors.New("collaborator not found")
	// ErrCollaboratorExist is returned when a collaborator already exists.
	ErrCollaboratorExist = errors.New("collaborator already exists")
	// ErrExceedsAccessLevel is returned when an action would grant or revoke
	// access above the caller's own access level.
	ErrExceedsAccessLevel = errors.New("cannot change access above your own access level")
)
