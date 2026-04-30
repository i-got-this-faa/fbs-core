package auth

import (
	"context"
	"testing"
)

func TestWithPrincipalAndPrincipalFromContext(t *testing.T) {
	t.Parallel()

	p := Principal{
		UserID:      "user-1",
		DisplayName: "Alice",
		AccessKeyID: "fbsa_abc123",
		Role:        "admin",
		DevMode:     false,
	}

	ctx := context.Background()
	ctx = WithPrincipal(ctx, p)

	got, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("expected principal to be found in context")
	}
	if got != p {
		t.Fatalf("principal mismatch: got %+v, want %+v", got, p)
	}
}

func TestPrincipalFromContext_Missing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, ok := PrincipalFromContext(ctx)
	if ok {
		t.Fatal("expected no principal in empty context")
	}
}
