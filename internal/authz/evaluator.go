package authz

import (
	"context"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/auth"
)

// DecisionRequest is the input to authorization evaluation.
type DecisionRequest struct {
	Principal  auth.Principal
	Action     string
	Bucket     string
	ObjectKey  string
	// ListPrefix is the list API prefix query value. Used only for s3:ListBucket.
	ListPrefix string
	// BucketOwnerID is the owner of Bucket when the bucket exists.
	// Empty when the operation does not target an existing bucket (CreateBucket).
	BucketOwnerID string
}

// GrantStore loads active grants for evaluation. Implementations must not
// return inactive grants (or must mark them Active=false).
type GrantStore interface {
	ListActiveForGranteeBucket(ctx context.Context, granteeUserID, bucketName string) ([]Grant, error)
}

// Evaluator decides allow/deny for S3 data-plane actions.
// It does not write HTTP responses and does not depend on net/http.
type Evaluator struct {
	Grants GrantStore
}

// Allow evaluates the request and returns whether access is permitted.
// Store errors are returned to the caller; callers must fail closed (deny or 5xx).
func (e *Evaluator) Allow(ctx context.Context, req DecisionRequest) (bool, error) {
	if strings.TrimSpace(req.Principal.UserID) == "" {
		return false, nil
	}
	if strings.TrimSpace(req.Action) == "" {
		return false, nil
	}

	// 1. System admin short-circuit.
	if req.Principal.Role == "admin" {
		return true, nil
	}

	// CreateBucket: any authenticated principal may create; not grant-based.
	if req.Action == ActionCreateBucket {
		return true, nil
	}

	// Operations on an existing bucket require a bucket identity.
	if strings.TrimSpace(req.Bucket) == "" {
		return false, nil
	}

	// 2. Bucket owner short-circuit (including DeleteBucket).
	if req.BucketOwnerID != "" && req.BucketOwnerID == req.Principal.UserID {
		return true, nil
	}

	// DeleteBucket is never grantable.
	if req.Action == ActionDeleteBucket {
		return false, nil
	}

	// 3. Matching active grant.
	if e == nil || e.Grants == nil {
		return false, nil
	}

	grants, err := e.Grants.ListActiveForGranteeBucket(ctx, req.Principal.UserID, req.Bucket)
	if err != nil {
		return false, err
	}

	for _, grant := range grants {
		if !grant.Active {
			continue
		}
		if grant.Action != req.Action {
			continue
		}
		if grantMatches(grant, req) {
			return true, nil
		}
	}

	// 4. Default deny.
	return false, nil
}

func grantMatches(grant Grant, req DecisionRequest) bool {
	switch req.Action {
	case ActionListBucket:
		return ListPrefixCovered(grant.KeyPrefix, req.ListPrefix)
	default:
		return PrefixMatches(grant.KeyPrefix, req.ObjectKey)
	}
}
