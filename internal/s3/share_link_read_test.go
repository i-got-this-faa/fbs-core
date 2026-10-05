package s3

import (
	"context"
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/iam"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

func (e objectTestEnv) mustCreateShareLink(t *testing.T, link metadata.ShareLink) {
	t.Helper()
	if link.BucketName == "" {
		link.BucketName = e.bucket
	}
	if link.CreatedBy == "" {
		link.CreatedBy = e.userID
	}
	link.CreatedAt = e.now
	if err := e.shareLinks.Create(context.Background(), &link); err != nil {
		t.Fatalf("create share link %s: %v", link.Code, err)
	}
}

func (e objectTestEnv) mustCreateUser(t *testing.T, role iam.Role) *metadata.User {
	t.Helper()
	_, _, user, err := auth.CreateBearerToken(context.Background(), e.users, "Share Creator", role)
	if err != nil {
		t.Fatalf("create %s user: %v", role, err)
	}
	return user
}

func assertShareLinkNotFound(t *testing.T, env objectTestEnv, path string) {
	t.Helper()
	resp := env.do(t, http.MethodGet, path, "", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("GET %s status = %d, want 404; body=%s", path, resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET %s Cache-Control = %q, want no-store", path, got)
	}
}

func TestShareLinkServesObjectAsDirectMedia(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/clips/clip.mp4", "fake video bytes", map[string]string{"Content-Type": "video/mp4"})
	if resp.Code != http.StatusOK {
		t.Fatalf("put status = %d", resp.Code)
	}
	env.mustCreateShareLink(t, metadata.ShareLink{Code: "clip", ObjectKey: "clips/clip.mp4"})

	for _, path := range []string{"/s/clip", "/s/clip/clip.mp4"} {
		resp := env.do(t, http.MethodGet, path, "", nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", path, resp.Code, resp.Body.String())
		}
		if resp.Body.String() != "fake video bytes" {
			t.Fatalf("GET %s body = %q", path, resp.Body.String())
		}
		if got := resp.Header().Get("Content-Type"); got != "video/mp4" {
			t.Fatalf("GET %s Content-Type = %q, want video/mp4", path, got)
		}
		if got := resp.Header().Get("Cache-Control"); got != shareLinkCacheControl {
			t.Fatalf("GET %s Cache-Control = %q, want %q", path, got, shareLinkCacheControl)
		}
		if got := resp.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("GET %s X-Content-Type-Options = %q, want nosniff", path, got)
		}
		if got := resp.Header().Get("Location"); got != "" {
			t.Fatalf("GET %s must not redirect, Location = %q", path, got)
		}
	}

	rangeResp := env.do(t, http.MethodGet, "/s/clip", "", map[string]string{"Range": "bytes=0-3"})
	if rangeResp.Code != http.StatusPartialContent || rangeResp.Body.String() != "fake" {
		t.Fatalf("range status = %d body = %q, want 206 fake", rangeResp.Code, rangeResp.Body.String())
	}

	headResp := env.do(t, http.MethodHead, "/s/clip", "", nil)
	if headResp.Code != http.StatusOK || headResp.Body.Len() != 0 {
		t.Fatalf("HEAD status = %d body length = %d, want 200 and empty", headResp.Code, headResp.Body.Len())
	}
	if got := headResp.Header().Get("Content-Length"); got != "16" {
		t.Fatalf("HEAD Content-Length = %q, want 16", got)
	}
}

func TestShareLinkAppliesStoredContentDisposition(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "report.pdf", "pdf")
	env.mustCreateShareLink(t, metadata.ShareLink{
		Code:                       "report",
		ObjectKey:                  "report.pdf",
		ResponseContentDisposition: `attachment; filename="report.pdf"`,
	})

	resp := env.do(t, http.MethodGet, "/s/report", "", nil)
	if got := resp.Header().Get("Content-Disposition"); got != `attachment; filename="report.pdf"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
}

func TestShareLinkUnknownAndExpiredReturnNotFound(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "body")
	expired := env.now
	notYet := env.now.Add(time.Second)
	env.mustCreateShareLink(t, metadata.ShareLink{Code: "expired", ObjectKey: "a.txt", ExpiresAt: &expired})
	env.mustCreateShareLink(t, metadata.ShareLink{Code: "fresh", ObjectKey: "a.txt", ExpiresAt: &notYet})

	assertShareLinkNotFound(t, env, "/s/unknown")
	assertShareLinkNotFound(t, env, "/s/expired")
	if resp := env.do(t, http.MethodGet, "/s/fresh", "", nil); resp.Code != http.StatusOK {
		t.Fatalf("unexpired link status = %d, want 200", resp.Code)
	}
}

func TestShareLinkFollowsKeyAcrossOverwriteAndDelete(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "first")
	env.mustCreateShareLink(t, metadata.ShareLink{Code: "follow", ObjectKey: "a.txt"})

	env.mustPut(t, "a.txt", "second")
	if resp := env.do(t, http.MethodGet, "/s/follow", "", nil); resp.Body.String() != "second" {
		t.Fatalf("after overwrite body = %q, want second", resp.Body.String())
	}

	if resp := env.do(t, http.MethodDelete, "/"+env.bucket+"/a.txt", "", nil); resp.Code != http.StatusNoContent {
		t.Fatalf("S3 delete must not be blocked by a share link, status = %d", resp.Code)
	}
	assertShareLinkNotFound(t, env, "/s/follow")
}

func TestShareLinkDisabledWhenCreatorDeactivated(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "body")
	creator := env.mustCreateUser(t, "admin")
	env.mustCreateShareLink(t, metadata.ShareLink{Code: "byadmin", ObjectKey: "a.txt", CreatedBy: creator.ID})

	if resp := env.do(t, http.MethodGet, "/s/byadmin", "", nil); resp.Code != http.StatusOK {
		t.Fatalf("active creator status = %d, want 200", resp.Code)
	}

	creator.IsActive = false
	if err := env.users.Update(context.Background(), creator); err != nil {
		t.Fatalf("deactivate creator: %v", err)
	}
	assertShareLinkNotFound(t, env, "/s/byadmin")
}

func TestShareLinkRequiresCreatorReadAccess(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "body")
	member := env.mustCreateUser(t, "member")
	env.mustCreateShareLink(t, metadata.ShareLink{Code: "bymember", ObjectKey: "a.txt", CreatedBy: member.ID})

	assertShareLinkNotFound(t, env, "/s/bymember")

	now := time.Now().UTC()
	grant := &metadata.Grant{
		ID:            uuid.New().String(),
		BucketName:    env.bucket,
		GranteeUserID: member.ID,
		Action:        "s3:GetObject",
		IsActive:      true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := env.grants.Create(context.Background(), grant); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	if resp := env.do(t, http.MethodGet, "/s/bymember", "", nil); resp.Code != http.StatusOK {
		t.Fatalf("granted member link status = %d, want 200", resp.Code)
	}
	grant.IsActive = false
	if err := env.grants.Update(context.Background(), grant); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	assertShareLinkNotFound(t, env, "/s/bymember")
}

func TestShareLinkRouteCannotShadowABucket(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/s", "", nil)
	if resp.Code == http.StatusOK {
		t.Fatal("bucket named \"s\" must be rejected so /s/ never shadows a bucket")
	}
}
