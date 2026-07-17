package s3

import (
	"testing"

	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// TestGrantableActionsInSync keeps metadata's grantable-action set aligned with
// authz.GrantableActions. The two packages cannot share a single source without
// an import cycle (auth → metadata → authz → auth).
func TestGrantableActionsInSync(t *testing.T) {
	t.Parallel()

	fromMetadata := map[string]struct{}{}
	for _, action := range metadata.GrantableActions() {
		fromMetadata[action] = struct{}{}
	}

	for action := range authz.GrantableActions {
		if _, ok := fromMetadata[action]; !ok {
			t.Errorf("action %q is in authz.GrantableActions but missing from metadata.GrantableActions", action)
		}
	}
	for action := range fromMetadata {
		if _, ok := authz.GrantableActions[action]; !ok {
			t.Errorf("action %q is in metadata.GrantableActions but missing from authz.GrantableActions", action)
		}
	}
}
