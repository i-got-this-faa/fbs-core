package authz

import "github.com/i-got-this-faa/fbs/internal/iam"

// Grant is the authorization view of a persisted resource grant.
// Metadata repositories map store rows into this shape.
type Grant struct {
	BucketName    string
	GranteeUserID string
	Action        iam.Action
	KeyPrefix     string
	Active        bool
}
