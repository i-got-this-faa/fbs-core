package management_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func TestManagementAdminCreateAndListGrants(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)

	body := strings.NewReader(`{
		"grantee_user_id":"` + env.memberUserID + `",
		"actions":["s3:GetObject","s3:ListBucket"],
		"key_prefix":"2026/",
		"note":"read photos"
	}`)
	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/grants", env.adminToken, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	var created struct {
		Grants []struct {
			ID            string `json:"id"`
			Action        string `json:"action"`
			GranteeUserID string `json:"grantee_user_id"`
			KeyPrefix     string `json:"key_prefix"`
		} `json:"grants"`
	}
	decodeResponse(t, resp, &created)
	if len(created.Grants) != 2 {
		t.Fatalf("grants = %+v, want 2", created.Grants)
	}

	list := env.do(t, http.MethodGet, "/api/management/buckets/photos/grants", env.adminToken, nil)
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", list.StatusCode)
	}
	var listed struct {
		Grants []json.RawMessage `json:"grants"`
	}
	decodeResponse(t, list, &listed)
	if len(listed.Grants) != 2 {
		t.Fatalf("listed len = %d", len(listed.Grants))
	}

	// Idempotent create returns existing.
	again := env.do(t, http.MethodPost, "/api/management/buckets/photos/grants", env.adminToken, strings.NewReader(`{
		"grantee_user_id":"`+env.memberUserID+`",
		"actions":["s3:GetObject"],
		"key_prefix":"2026/"
	}`))
	defer again.Body.Close()
	if again.StatusCode != http.StatusCreated {
		t.Fatalf("idempotent status = %d body=%s", again.StatusCode, readBody(t, again))
	}
}

func TestManagementMemberCannotMutateForeignGrants(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/grants", env.memberToken, strings.NewReader(`{
		"grantee_user_id":"`+env.memberUserID+`",
		"actions":["s3:GetObject"]
	}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestManagementMemberListMyGrants(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	create := env.do(t, http.MethodPost, "/api/management/buckets/photos/grants", env.adminToken, strings.NewReader(`{
		"grantee_user_id":"`+env.memberUserID+`",
		"actions":["s3:GetObject"]
	}`))
	defer create.Body.Close()
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.StatusCode, readBody(t, create))
	}

	resp := env.do(t, http.MethodGet, "/api/management/grants/me", env.memberToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		Grants []struct {
			Bucket string `json:"bucket"`
			Action string `json:"action"`
		} `json:"grants"`
	}
	decodeResponse(t, resp, &body)
	if len(body.Grants) != 1 || body.Grants[0].Bucket != "photos" {
		t.Fatalf("my grants = %+v", body.Grants)
	}
}

func TestManagementRejectNonGrantableAction(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/grants", env.adminToken, strings.NewReader(`{
		"grantee_user_id":"`+env.memberUserID+`",
		"actions":["s3:DeleteBucket"]
	}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestManagementOwnerCanGrantOnOwnBucket(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	// Create a bucket owned by the member.
	if err := metadata.NewBucketRepository(env.db).Create(context.Background(), &metadata.Bucket{
		Name:      "member-owned",
		OwnerID:   env.memberUserID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	resp := env.do(t, http.MethodPost, "/api/management/buckets/member-owned/grants", env.memberToken, strings.NewReader(`{
		"grantee_user_id":"`+env.adminUserID+`",
		"actions":["s3:ListBucket"]
	}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestManagementTransferOwnership(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	resp := env.do(t, http.MethodPost, "/api/management/buckets/photos/transfer-ownership", env.adminToken, strings.NewReader(`{
		"new_owner_user_id":"`+env.memberUserID+`"
	}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	bucket, err := metadata.NewBucketRepository(env.db).GetByName(context.Background(), "photos")
	if err != nil {
		t.Fatalf("get bucket: %v", err)
	}
	if bucket.OwnerID != env.memberUserID {
		t.Fatalf("owner = %s, want %s", bucket.OwnerID, env.memberUserID)
	}
}

func TestManagementDeleteGrant(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	create := env.do(t, http.MethodPost, "/api/management/buckets/photos/grants", env.adminToken, strings.NewReader(`{
		"grantee_user_id":"`+env.memberUserID+`",
		"actions":["s3:GetObject"]
	}`))
	defer create.Body.Close()
	var created struct {
		Grants []struct {
			ID string `json:"id"`
		} `json:"grants"`
	}
	decodeResponse(t, create, &created)
	if len(created.Grants) != 1 {
		t.Fatalf("created = %+v", created)
	}

	del := env.do(t, http.MethodDelete, "/api/management/buckets/photos/grants/"+created.Grants[0].ID, env.adminToken, nil)
	defer del.Body.Close()
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", del.StatusCode, readBody(t, del))
	}
}
