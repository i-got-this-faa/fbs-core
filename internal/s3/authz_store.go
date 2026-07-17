package s3

import (
	"context"

	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

// NewAuthzEvaluator builds an evaluator backed by the grant repository.
func NewAuthzEvaluator(grants metadata.GrantRepository) *authz.Evaluator {
	if grants == nil {
		return &authz.Evaluator{}
	}
	return &authz.Evaluator{
		Grants: authz.FuncStore(func(ctx context.Context, granteeUserID, bucketName string) ([]authz.Grant, error) {
			rows, err := grants.ListActiveForGranteeBucket(ctx, granteeUserID, bucketName)
			if err != nil {
				return nil, err
			}
			out := make([]authz.Grant, 0, len(rows))
			for _, row := range rows {
				out = append(out, authz.Grant{
					BucketName:    row.BucketName,
					GranteeUserID: row.GranteeUserID,
					Action:        row.Action,
					KeyPrefix:     row.KeyPrefix,
					Active:        row.IsActive,
				})
			}
			return out, nil
		}),
	}
}
