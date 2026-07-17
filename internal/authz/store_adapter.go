package authz

import "context"

// FuncStore adapts a function to GrantStore.
type FuncStore func(ctx context.Context, granteeUserID, bucketName string) ([]Grant, error)

// ListActiveForGranteeBucket implements GrantStore.
func (f FuncStore) ListActiveForGranteeBucket(ctx context.Context, granteeUserID, bucketName string) ([]Grant, error) {
	if f == nil {
		return nil, nil
	}
	return f(ctx, granteeUserID, bucketName)
}
