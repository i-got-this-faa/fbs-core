package s3

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func TestGranteeCanGetObjectWithGrant(t *testing.T) {
	t.Parallel()

	env := newScopedS3TestEnv(t, auth.Principal{
		UserID:      "member-user",
		DisplayName: "Member User",
		AccessKeyID: "member",
		Role:        "member",
	})

	now := time.Now().UTC()
	storagePath, size, err := env.storage.Write(context.Background(), "other-bucket", "shared2.txt", strings.NewReader("data"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := env.objects.Create(context.Background(), &metadata.Object{
		ID: uuid.NewString(), BucketName: "other-bucket", Key: "shared2.txt",
		Size: size, ETag: "e", ContentType: "text/plain", StoragePath: storagePath,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create object metadata: %v", err)
	}

	if env.grants == nil {
		t.Fatal("grants repo missing")
	}
	_, _, err = env.grants.CreateIdempotent(context.Background(), &metadata.Grant{
		ID: uuid.NewString(), BucketName: "other-bucket", GranteeUserID: "member-user",
		Action: authz.ActionGetObject, IsActive: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}

	put := env.do(t, http.MethodPut, "/other-bucket/nope.txt", "x", nil)
	if put.Code != http.StatusForbidden {
		t.Fatalf("put status = %d, want 403", put.Code)
	}

	get := env.do(t, http.MethodGet, "/other-bucket/shared2.txt", "", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", get.Code, get.Body.String())
	}
	if get.Body.String() != "data" {
		t.Fatalf("body = %q", get.Body.String())
	}
}

func TestListBucketsIncludesGrantedBuckets(t *testing.T) {
	t.Parallel()

	env := newScopedS3TestEnv(t, auth.Principal{
		UserID:      "member-user",
		DisplayName: "Member User",
		AccessKeyID: "member",
		Role:        "member",
	})

	now := time.Now().UTC()
	_, _, err := env.grants.CreateIdempotent(context.Background(), &metadata.Grant{
		ID: uuid.NewString(), BucketName: "other-bucket", GranteeUserID: "member-user",
		Action: authz.ActionGetObject, IsActive: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}

	list := env.do(t, http.MethodGet, "/", "", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", list.Code, list.Body.String())
	}
	var result listAllMyBucketsResult
	if err := xml.Unmarshal(list.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	names := map[string]bool{}
	for _, b := range result.Buckets.Buckets {
		names[b.Name] = true
	}
	if !names["member-bucket"] || !names["other-bucket"] {
		t.Fatalf("buckets = %v, want member-bucket and other-bucket", names)
	}
}

func TestPrefixLimitedPutDeniedOutsidePrefix(t *testing.T) {
	t.Parallel()

	env := newScopedS3TestEnv(t, auth.Principal{
		UserID:      "member-user",
		DisplayName: "Member User",
		AccessKeyID: "member",
		Role:        "member",
	})

	now := time.Now().UTC()
	for _, action := range []string{authz.ActionPutObject, authz.ActionListBucket} {
		_, _, err := env.grants.CreateIdempotent(context.Background(), &metadata.Grant{
			ID: uuid.NewString(), BucketName: "other-bucket", GranteeUserID: "member-user",
			Action: action, KeyPrefix: "uploads/", IsActive: true,
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("create grant %s: %v", action, err)
		}
	}

	allowed := env.do(t, http.MethodPut, "/other-bucket/uploads/a.txt", "ok", nil)
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed put status = %d; body=%s", allowed.Code, allowed.Body.String())
	}

	denied := env.do(t, http.MethodPut, "/other-bucket/secret.txt", "no", nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied put status = %d, want 403", denied.Code)
	}
}
