// Package ops is everything a caller can do to repositories, their own
// account and, for admins, the server's users and settings, with the caller's permissions applied, independent of how they asked.
//
// The SSH commands, the REST API, the web UI and the TUI all go through a
// Service, so a rule about who may do what lives in one place and every
// surface obeys it. Local implements Service on the server, over the
// backend. The soft client implements it over the REST API, which is itself
// served by Local, so the client gets the same answers as everyone else.
package ops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// Actor is who is asking.
type Actor struct {
	// User is the Soft Serve user whose access applies; nil means
	// anonymous, with the server's anon-access level.
	User proto.User
	// Admin is whether the caller is a server administrator: a user with
	// the admin flag, or over SSH, someone using one of the configured
	// admin keys. Nothing is hidden from an admin.
	Admin bool
}

// UserActor is the actor for user, a server admin if the user is one.
func UserActor(user proto.User) Actor {
	return Actor{User: user, Admin: user != nil && user.IsAdmin()}
}

// Service is the repository operations one actor may perform. Every method
// checks the actor's access; a repository the actor cannot read is reported
// the same way whether or not it exists.
type Service interface {
	// ListRepos lists the repositories the actor can read, hidden ones
	// included only when all is set.
	ListRepos(ctx context.Context, all bool) ([]RepoSummary, error)
	// Repo describes one repository.
	Repo(ctx context.Context, name string) (Repo, error)
	// CreateRepo creates a repository, or imports one when opts.Remote is
	// set: a hard fork, or with opts.Mirror a mirror that follows it.
	CreateRepo(ctx context.Context, opts CreateRepo) (Repo, error)
	// DeleteRepo deletes a repository.
	DeleteRepo(ctx context.Context, name string) error
	// RenameRepo renames a repository. The new name is gated like a new
	// repository, since it may already be someone else's.
	RenameRepo(ctx context.Context, name, newName string) error
	// UpdateRepo changes the settings that are set in u, in order, and
	// stops at the first that fails.
	UpdateRepo(ctx context.Context, name string, u RepoUpdate) error
	// SyncMirror pulls a mirror from its upstream now.
	SyncMirror(ctx context.Context, name string) error

	// DeleteBranch deletes a branch other than the default one.
	DeleteBranch(ctx context.Context, name, branch string) error
	// DeleteTag deletes a tag.
	DeleteTag(ctx context.Context, name, tag string) error

	// Collaborators lists a repository's collaborators and their access.
	Collaborators(ctx context.Context, name string) ([]Collaborator, error)
	// AddCollaborator adds a collaborator; level defaults to read-write.
	// Nobody but a server admin can grant more access than they have, or
	// replace someone who has more. To change a collaborator's access,
	// remove and add them again.
	AddCollaborator(ctx context.Context, name, username, level string) error
	// RemoveCollaborator removes a collaborator, under the same rule:
	// nobody but a server admin can remove someone with more access.
	RemoveCollaborator(ctx context.Context, name, username string) error

	// Webhooks lists a repository's webhooks. Secrets are never returned.
	Webhooks(ctx context.Context, name string) ([]Webhook, error)
	// CreateWebhook adds a webhook.
	CreateWebhook(ctx context.Context, name string, opts WebhookOptions) (Webhook, error)
	// UpdateWebhook changes the fields that are set in opts.
	UpdateWebhook(ctx context.Context, name string, id int64, opts WebhookOptions) error
	// DeleteWebhook removes a webhook.
	DeleteWebhook(ctx context.Context, name string, id int64) error
	// Deliveries lists a webhook's deliveries.
	Deliveries(ctx context.Context, name string, id int64) ([]Delivery, error)
	// Delivery is one delivery in full.
	Delivery(ctx context.Context, name string, id int64, deliveryID string) (DeliveryDetail, error)
	// Redeliver sends a delivery again.
	Redeliver(ctx context.Context, name string, id int64, deliveryID string) error

	// Account is the actor's own account. Like the rest of the account
	// methods, it needs a signed-in actor and acts only on their account.
	Account(ctx context.Context) (Account, error)
	// AddPublicKey adds an SSH key, given in authorized_keys form.
	AddPublicKey(ctx context.Context, key string) (PublicKey, error)
	// RemovePublicKey removes an SSH key, given in authorized_keys form or
	// as its SHA256 fingerprint.
	RemovePublicKey(ctx context.Context, key string) error
	// AccessTokens lists the actor's access tokens.
	AccessTokens(ctx context.Context) ([]AccessToken, error)
	// CreateAccessToken creates an access token, which is returned once.
	CreateAccessToken(ctx context.Context, opts CreateToken) (NewToken, error)
	// DeleteAccessToken deletes one of the actor's access tokens.
	DeleteAccessToken(ctx context.Context, id int64) error
	// SetUsername renames the actor's account.
	SetUsername(ctx context.Context, username string) error

	// Users lists the server's users. This and the rest of the user and
	// settings methods are for server admins only.
	Users(ctx context.Context) ([]UserSummary, error)
	// User describes one user.
	User(ctx context.Context, name string) (Account, error)
	// CreateUser creates a user.
	CreateUser(ctx context.Context, opts CreateUser) (Account, error)
	// DeleteUser deletes a user who owns no repositories. Admins can't
	// delete themselves.
	DeleteUser(ctx context.Context, name string) error
	// UpdateUser changes the admin flag, then the username, as set in u.
	// Admins can't remove their own admin flag.
	UpdateUser(ctx context.Context, name string, u UserUpdate) error
	// AddUserKey adds an SSH key to a user.
	AddUserKey(ctx context.Context, name, key string) (PublicKey, error)
	// RemoveUserKey removes an SSH key, or the key with a fingerprint,
	// from a user.
	RemoveUserKey(ctx context.Context, name, key string) error
	// Settings are the server's settings.
	Settings(ctx context.Context) (Settings, error)
	// UpdateSettings changes the settings set in u, and returns them all.
	UpdateSettings(ctx context.Context, u SettingsUpdate) (Settings, error)
}

// RepoSummary is a repository in a list.
type RepoSummary struct {
	Name        string `json:"name"`
	ProjectName string `json:"project_name,omitempty"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
	Hidden      bool   `json:"hidden"`
	Mirror      bool   `json:"mirror"`
}

// Repo is a repository's settings and refs, as `repo info` shows them.
type Repo struct {
	RepoSummary
	// AutoSync is set for a mirror: whether it syncs on the server's
	// schedule or only when asked.
	AutoSync *bool  `json:"auto_sync,omitempty"`
	Owner    string `json:"owner,omitempty"`
	// SSHURL is the repository's clone URL over SSH.
	SSHURL string `json:"ssh_url,omitempty"`
	// DefaultBranch is empty for a repository with no commits.
	DefaultBranch string   `json:"default_branch,omitempty"`
	Branches      []string `json:"branches"`
	Tags          []string `json:"tags"`
	// Access is the actor's: read-only, read-write or admin-access.
	Access string `json:"access"`
}

// CreateRepo describes a repository to create or import.
type CreateRepo struct {
	Name        string `json:"name"`
	ProjectName string `json:"project_name,omitempty"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private,omitempty"`
	Hidden      bool   `json:"hidden,omitempty"`

	// Remote makes this an import from that URL.
	Remote string `json:"remote,omitempty"`
	// Mirror keeps an import following Remote. Without it the import is
	// a hard fork.
	Mirror bool `json:"mirror,omitempty"`
	// AutoSync, for a mirror, is whether it syncs on the server's
	// schedule. Unset means yes.
	AutoSync    *bool  `json:"auto_sync,omitempty"`
	LFS         bool   `json:"lfs,omitempty"`
	LFSEndpoint string `json:"lfs_endpoint,omitempty"`
}

// RepoUpdate is the settings to change; nil fields stay as they are.
type RepoUpdate struct {
	ProjectName   *string `json:"project_name,omitempty"`
	Description   *string `json:"description,omitempty"`
	Private       *bool   `json:"private,omitempty"`
	Hidden        *bool   `json:"hidden,omitempty"`
	Mirror        *bool   `json:"mirror,omitempty"`
	AutoSync      *bool   `json:"auto_sync,omitempty"`
	DefaultBranch *string `json:"default_branch,omitempty"`
}

// Collaborator is someone with access of their own to a repository.
type Collaborator struct {
	Username string `json:"username"`
	Access   string `json:"access"`
}

// Webhook is a repository webhook, without its secret.
type Webhook struct {
	ID     int64    `json:"id"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
	// ContentType is json or form.
	ContentType string    `json:"content_type"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WebhookOptions describes a webhook to create, or the changes to make to
// one. When updating, empty or nil fields stay as they are. When creating,
// ContentType defaults to json and Active to true.
type WebhookOptions struct {
	URL         string   `json:"url,omitempty"`
	Events      []string `json:"events,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	Secret      string   `json:"secret,omitempty"`
	Active      *bool    `json:"active,omitempty"`
}

// Delivery is one attempt to deliver a webhook event.
type Delivery struct {
	ID             string    `json:"id"`
	Event          string    `json:"event"`
	ResponseStatus int       `json:"response_status"`
	CreatedAt      time.Time `json:"created_at"`
}

// OK reports whether the receiver accepted the delivery.
func (d Delivery) OK() bool {
	return d.ResponseStatus >= 200 && d.ResponseStatus < 300
}

// DeliveryDetail is a delivery with its request and response.
type DeliveryDetail struct {
	Delivery
	RequestURL      string `json:"request_url"`
	RequestMethod   string `json:"request_method"`
	RequestError    string `json:"request_error,omitempty"`
	RequestHeaders  string `json:"request_headers"`
	RequestBody     string `json:"request_body"`
	ResponseHeaders string `json:"response_headers"`
	ResponseBody    string `json:"response_body"`
}

// InvalidError is a request that cannot be carried out as asked: a bad
// value, or a change the repository does not allow. Its message is safe to
// show the caller.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

// Invalid returns an InvalidError.
func Invalid(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err is an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}
