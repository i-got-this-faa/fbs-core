package objectops

import (
	"context"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// RecordActivity stores an audit event attributed to the request principal.
// A nil repository disables activity recording.
func RecordActivity(ctx context.Context, repo metadata.ActivityRepository, event metadata.ObjectActivity) error {
	if repo == nil {
		return nil
	}
	if principal, ok := auth.PrincipalFromContext(ctx); ok {
		event.ActorUserID = principal.UserID
	}
	return repo.Create(ctx, &event)
}
