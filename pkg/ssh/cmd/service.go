package cmd

import (
	"context"

	"github.com/charmbracelet/soft-serve/pkg/backend"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/proto"
)

// serviceKey holds the ops.Service the repository commands act through.
type serviceKey struct{}

// WithService makes the repository commands act through svc. The soft
// client uses it to run the same commands against a server's REST API.
func WithService(ctx context.Context, svc ops.Service) context.Context {
	return context.WithValue(ctx, serviceKey{}, svc)
}

// service is what the repository commands act through: the one put in the
// context, or on the server, the backend acting for the session's caller.
func service(ctx context.Context) ops.Service {
	if svc, ok := ctx.Value(serviceKey{}).(ops.Service); ok {
		return svc
	}
	return ops.NewLocal(backend.FromContext(ctx), Actor(ctx))
}

// Actor is who an SSH session is acting for. A caller holding one of the
// configured admin keys is a server admin whatever their account says.
func Actor(ctx context.Context) ops.Actor {
	return ops.Actor{User: proto.UserFromContext(ctx), Admin: isServerAdmin(ctx)}
}
