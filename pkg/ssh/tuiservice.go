package ssh

import (
	"context"
	"sync"

	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// sessionService is the ops.Service a TUI session acts through. It is
// ops.Local for the session's caller, except that it follows the caller
// renaming themselves.
//
// A TUI session lives a long time, and the access checks look the caller
// up as collaborator by the username on the user they were given. After
// SetUsername that user is stale: it still has the old name, and the
// caller would lose access to every repository they collaborate on for the
// rest of the session. So a successful SetUsername reads the user again
// and hands the fresh one to the Local it acts through and, through
// setUser, to the session's context.
type sessionService struct {
	be      *backend.Backend
	setUser func(proto.User)

	mu  sync.RWMutex
	svc *ops.Local
}

var _ ops.Service = (*sessionService)(nil)

func newSessionService(be *backend.Backend, actor ops.Actor, setUser func(proto.User)) *sessionService {
	return &sessionService{be: be, setUser: setUser, svc: ops.NewLocal(be, actor)}
}

func (s *sessionService) local() *ops.Local {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.svc
}

// refresh reads the caller's user again and acts as it from now on.
func (s *sessionService) refresh(ctx context.Context) error {
	actor := s.local().Actor()
	if actor.User == nil {
		return nil
	}
	u, err := s.be.UserByID(ctx, actor.User.ID())
	if err != nil {
		return err
	}
	// The admin flag stays as the session worked it out: it may come from
	// an admin key rather than the account, and a rename changes neither.
	actor.User = u
	s.mu.Lock()
	s.svc = ops.NewLocal(s.be, actor)
	s.mu.Unlock()
	if s.setUser != nil {
		s.setUser(u)
	}
	return nil
}

// SetUsername implements ops.Service, and then follows the new name.
func (s *sessionService) SetUsername(ctx context.Context, username string) error {
	if err := s.local().SetUsername(ctx, username); err != nil {
		return err
	}
	return s.refresh(ctx)
}

// ListRepos implements ops.Service.
func (s *sessionService) ListRepos(ctx context.Context, all bool) ([]ops.RepoSummary, error) {
	return s.local().ListRepos(ctx, all)
}

// Repo implements ops.Service.
func (s *sessionService) Repo(ctx context.Context, name string) (ops.Repo, error) {
	return s.local().Repo(ctx, name)
}

// CreateRepo implements ops.Service.
func (s *sessionService) CreateRepo(ctx context.Context, opts ops.CreateRepo) (ops.Repo, error) {
	return s.local().CreateRepo(ctx, opts)
}

// DeleteRepo implements ops.Service.
func (s *sessionService) DeleteRepo(ctx context.Context, name string) error {
	return s.local().DeleteRepo(ctx, name)
}

// RenameRepo implements ops.Service.
func (s *sessionService) RenameRepo(ctx context.Context, name, newName string) error {
	return s.local().RenameRepo(ctx, name, newName)
}

// UpdateRepo implements ops.Service.
func (s *sessionService) UpdateRepo(ctx context.Context, name string, u ops.RepoUpdate) error {
	return s.local().UpdateRepo(ctx, name, u)
}

// SyncMirror implements ops.Service.
func (s *sessionService) SyncMirror(ctx context.Context, name string) error {
	return s.local().SyncMirror(ctx, name)
}

// DeleteBranch implements ops.Service.
func (s *sessionService) DeleteBranch(ctx context.Context, name, branch string) error {
	return s.local().DeleteBranch(ctx, name, branch)
}

// DeleteTag implements ops.Service.
func (s *sessionService) DeleteTag(ctx context.Context, name, tag string) error {
	return s.local().DeleteTag(ctx, name, tag)
}

// Collaborators implements ops.Service.
func (s *sessionService) Collaborators(ctx context.Context, name string) ([]ops.Collaborator, error) {
	return s.local().Collaborators(ctx, name)
}

// AddCollaborator implements ops.Service.
func (s *sessionService) AddCollaborator(ctx context.Context, name, username, level string) error {
	return s.local().AddCollaborator(ctx, name, username, level)
}

// RemoveCollaborator implements ops.Service.
func (s *sessionService) RemoveCollaborator(ctx context.Context, name, username string) error {
	return s.local().RemoveCollaborator(ctx, name, username)
}

// Webhooks implements ops.Service.
func (s *sessionService) Webhooks(ctx context.Context, name string) ([]ops.Webhook, error) {
	return s.local().Webhooks(ctx, name)
}

// CreateWebhook implements ops.Service.
func (s *sessionService) CreateWebhook(ctx context.Context, name string, opts ops.WebhookOptions) (ops.Webhook, error) {
	return s.local().CreateWebhook(ctx, name, opts)
}

// UpdateWebhook implements ops.Service.
func (s *sessionService) UpdateWebhook(ctx context.Context, name string, id int64, opts ops.WebhookOptions) error {
	return s.local().UpdateWebhook(ctx, name, id, opts)
}

// DeleteWebhook implements ops.Service.
func (s *sessionService) DeleteWebhook(ctx context.Context, name string, id int64) error {
	return s.local().DeleteWebhook(ctx, name, id)
}

// Deliveries implements ops.Service.
func (s *sessionService) Deliveries(ctx context.Context, name string, id int64) ([]ops.Delivery, error) {
	return s.local().Deliveries(ctx, name, id)
}

// Delivery implements ops.Service.
func (s *sessionService) Delivery(ctx context.Context, name string, id int64, deliveryID string) (ops.DeliveryDetail, error) {
	return s.local().Delivery(ctx, name, id, deliveryID)
}

// Redeliver implements ops.Service.
func (s *sessionService) Redeliver(ctx context.Context, name string, id int64, deliveryID string) error {
	return s.local().Redeliver(ctx, name, id, deliveryID)
}

// Account implements ops.Service.
func (s *sessionService) Account(ctx context.Context) (ops.Account, error) {
	return s.local().Account(ctx)
}

// AddPublicKey implements ops.Service.
func (s *sessionService) AddPublicKey(ctx context.Context, key string) (ops.PublicKey, error) {
	return s.local().AddPublicKey(ctx, key)
}

// RemovePublicKey implements ops.Service.
func (s *sessionService) RemovePublicKey(ctx context.Context, key string) error {
	return s.local().RemovePublicKey(ctx, key)
}

// AccessTokens implements ops.Service.
func (s *sessionService) AccessTokens(ctx context.Context) ([]ops.AccessToken, error) {
	return s.local().AccessTokens(ctx)
}

// CreateAccessToken implements ops.Service.
func (s *sessionService) CreateAccessToken(ctx context.Context, opts ops.CreateToken) (ops.NewToken, error) {
	return s.local().CreateAccessToken(ctx, opts)
}

// DeleteAccessToken implements ops.Service.
func (s *sessionService) DeleteAccessToken(ctx context.Context, id int64) error {
	return s.local().DeleteAccessToken(ctx, id)
}
