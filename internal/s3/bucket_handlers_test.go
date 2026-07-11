package s3

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/config"
	httpapi "github.com/i-got-this-faa/fbs/internal/http"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

func TestCreateBucket(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/created-bucket", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Location"); got != "/created-bucket" {
		t.Fatalf("Location = %q, want /created-bucket", got)
	}
	if resp.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0", resp.Body.Len())
	}

	bucket, err := env.buckets.GetByName(context.Background(), "created-bucket")
	if err != nil {
		t.Fatalf("get bucket: %v", err)
	}
	if bucket.OwnerID != "dev-user" {
		t.Fatalf("OwnerID = %q, want dev-user", bucket.OwnerID)
	}

	devUser, err := env.users.GetByID(context.Background(), "dev-user")
	if err != nil {
		t.Fatalf("get dev user: %v", err)
	}
	if devUser.DisplayName != "Development User" {
		t.Fatalf("DisplayName = %q, want Development User", devUser.DisplayName)
	}
}

func TestCreateBucketWithUsEast1LocationConstraint(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/regional-bucket", createBucketConfigXML("us-east-1"), map[string]string{
		"Content-Type": "application/xml",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
}

func TestCreateBucketAlreadyOwnedByYou(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	first := env.do(t, http.MethodPut, "/owned-bucket", "", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", first.Code, first.Body.String())
	}

	second := env.do(t, http.MethodPut, "/owned-bucket", "", nil)
	if second.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", second.Code, second.Body.String())
	}
	assertS3ErrorCode(t, second.Body.Bytes(), codeBucketAlreadyOwnedByYou)
}

func TestCreateBucketAlreadyExists(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	if err := env.buckets.Create(context.Background(), &metadata.Bucket{
		Name:      "shared-bucket",
		OwnerID:   env.userID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create existing bucket: %v", err)
	}

	resp := env.do(t, http.MethodPut, "/shared-bucket", "", nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeBucketAlreadyExists)
}

func TestCreateBucketInvalidName(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	for _, name := range []string{"Abc", "ab", "bucket..name", "bucket-.name", "192.168.0.1", "public"} {
		resp := env.do(t, http.MethodPut, "/"+name, "", nil)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("name %q status = %d, want 400; body=%s", name, resp.Code, resp.Body.String())
		}
		assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidBucketName)
	}
}

func TestCreateBucketMalformedXML(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/xml-bucket", "<CreateBucketConfiguration>", map[string]string{
		"Content-Type": "application/xml",
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeMalformedXML)
}

func TestCreateBucketInvalidLocationConstraint(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodPut, "/xml-bucket", createBucketConfigXML("eu-west-1"), map[string]string{
		"Content-Type": "application/xml",
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidLocationConstraint)
}

func TestListBuckets(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	if err := env.buckets.Create(context.Background(), &metadata.Bucket{
		Name:      "earlier-bucket",
		OwnerID:   env.userID,
		CreatedAt: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create earlier bucket: %v", err)
	}

	resp := env.do(t, http.MethodGet, "/", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	var result listAllMyBucketsResult
	if err := xml.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal list buckets: %v", err)
	}
	if result.Owner.ID != "dev-user" || result.Owner.DisplayName != "Development User" {
		t.Fatalf("owner = %+v, want dev user", result.Owner)
	}
	if len(result.Buckets.Buckets) < 2 || result.Buckets.Buckets[0].Name != "earlier-bucket" {
		t.Fatalf("buckets = %+v, want earlier-bucket first", result.Buckets.Buckets)
	}
}

func TestHeadBucket(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodHead, "/"+env.bucket, "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	if resp.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0", resp.Body.Len())
	}
	if got := resp.Header().Get("x-amz-bucket-region"); got != "us-east-1" {
		t.Fatalf("x-amz-bucket-region = %q, want us-east-1", got)
	}

	missing := env.do(t, http.MethodHead, "/missing-bucket", "", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404", missing.Code)
	}
	if missing.Body.Len() != 0 {
		t.Fatalf("missing body length = %d, want 0", missing.Body.Len())
	}
}

func TestMemberBucketAccessIsScopedToOwner(t *testing.T) {
	t.Parallel()

	env := newScopedS3TestEnv(t, auth.Principal{
		UserID:      "member-user",
		DisplayName: "Member User",
		AccessKeyID: "member",
		Role:        "member",
	})

	list := env.do(t, http.MethodGet, "/", "", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", list.Code, list.Body.String())
	}
	var result listAllMyBucketsResult
	if err := xml.Unmarshal(list.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal list buckets: %v", err)
	}
	if len(result.Buckets.Buckets) != 1 || result.Buckets.Buckets[0].Name != "member-bucket" {
		t.Fatalf("buckets = %+v, want only member-bucket", result.Buckets.Buckets)
	}

	owned := env.do(t, http.MethodHead, "/member-bucket", "", nil)
	if owned.Code != http.StatusOK {
		t.Fatalf("owned status = %d, want 200", owned.Code)
	}

	other := env.do(t, http.MethodHead, "/other-bucket", "", nil)
	if other.Code != http.StatusForbidden {
		t.Fatalf("other status = %d, want 403; body=%s", other.Code, other.Body.String())
	}

	putOther := env.do(t, http.MethodPut, "/other-bucket/object.txt", "body", nil)
	if putOther.Code != http.StatusForbidden {
		t.Fatalf("put other status = %d, want 403; body=%s", putOther.Code, putOther.Body.String())
	}
}

func TestDeleteBucket(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	if err := env.buckets.Create(context.Background(), &metadata.Bucket{
		Name:      "empty-bucket",
		OwnerID:   env.userID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create empty bucket: %v", err)
	}

	resp := env.do(t, http.MethodDelete, "/empty-bucket", "", nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", resp.Code, resp.Body.String())
	}
	if _, err := env.buckets.GetByName(context.Background(), "empty-bucket"); err != metadata.ErrBucketNotFound {
		t.Fatalf("bucket lookup err = %v, want ErrBucketNotFound", err)
	}

	env.mustPut(t, "still-here.txt", "body")
	nonEmpty := env.do(t, http.MethodDelete, "/"+env.bucket, "", nil)
	if nonEmpty.Code != http.StatusConflict {
		t.Fatalf("non-empty status = %d, want 409; body=%s", nonEmpty.Code, nonEmpty.Body.String())
	}
	assertS3ErrorCode(t, nonEmpty.Body.Bytes(), codeBucketNotEmpty)
}

func TestGetBucketLocation(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodGet, "/"+env.bucket+"?location", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	var result locationConstraintResult
	if err := xml.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal location: %v", err)
	}
	if result.Value != "" {
		t.Fatalf("LocationConstraint = %q, want empty for us-east-1", result.Value)
	}
}

func TestListObjectsV2(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "z.txt", "z")
	env.mustPut(t, "a.txt", "a")
	env.mustPut(t, "nested/file.txt", "nested")

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	result := decodeListBucketResult(t, resp.Body.Bytes())
	if result.Name != env.bucket {
		t.Fatalf("Name = %q, want %q", result.Name, env.bucket)
	}
	if result.KeyCount != 3 {
		t.Fatalf("KeyCount = %d, want 3", result.KeyCount)
	}
	if result.IsTruncated {
		t.Fatal("expected untruncated result")
	}

	gotKeys := listResultKeys(result)
	wantKeys := []string{"a.txt", "nested/file.txt", "z.txt"}
	assertStringSlice(t, gotKeys, wantKeys)
}

func TestListObjectsV1(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "a")
	env.mustPut(t, "b.txt", "b")
	env.mustPut(t, "docs/a.txt", "doc")

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"?prefix=&max-keys=2", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	result := decodeListBucketV1Result(t, resp.Body.Bytes())
	assertStringSlice(t, listV1ResultKeys(result), []string{"a.txt", "b.txt"})
	if !result.IsTruncated || result.NextMarker != "b.txt" {
		t.Fatalf("truncation = %v next=%q, want truncated next b.txt", result.IsTruncated, result.NextMarker)
	}

	second := env.do(t, http.MethodGet, "/"+env.bucket+"?marker="+result.NextMarker, "", nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", second.Code, second.Body.String())
	}
	assertStringSlice(t, listV1ResultKeys(decodeListBucketV1Result(t, second.Body.Bytes())), []string{"docs/a.txt"})

	delimited := env.do(t, http.MethodGet, "/"+env.bucket+"?delimiter=/", "", nil)
	if delimited.Code != http.StatusOK {
		t.Fatalf("delimited status = %d, want 200; body=%s", delimited.Code, delimited.Body.String())
	}
	delimitedResult := decodeListBucketV1Result(t, delimited.Body.Bytes())
	assertStringSlice(t, listV1ResultKeys(delimitedResult), []string{"a.txt", "b.txt"})
	assertStringSlice(t, listV1ResultPrefixes(delimitedResult), []string{"docs/"})
}

func TestListObjectsV1InvalidMaxKeys(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=1&max-keys=not-a-number", "", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidRequest)
}

func TestListObjectsV2Prefix(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "docs/a.txt", "a")
	env.mustPut(t, "docs/b.txt", "b")
	env.mustPut(t, "images/a.jpg", "jpg")

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&prefix=docs/", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	result := decodeListBucketResult(t, resp.Body.Bytes())
	if result.Prefix != "docs/" {
		t.Fatalf("Prefix = %q, want docs/", result.Prefix)
	}
	assertStringSlice(t, listResultKeys(result), []string{"docs/a.txt", "docs/b.txt"})
}

func TestListObjectsV2Pagination(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "a")
	env.mustPut(t, "b.txt", "b")
	env.mustPut(t, "c.txt", "c")

	firstResp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&max-keys=2", "", nil)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", firstResp.Code, firstResp.Body.String())
	}
	first := decodeListBucketResult(t, firstResp.Body.Bytes())
	if !first.IsTruncated {
		t.Fatal("expected first page to be truncated")
	}
	if first.NextContinuationToken == "" {
		t.Fatal("expected next continuation token")
	}
	assertStringSlice(t, listResultKeys(first), []string{"a.txt", "b.txt"})

	secondResp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&continuation-token="+first.NextContinuationToken, "", nil)
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", secondResp.Code, secondResp.Body.String())
	}
	second := decodeListBucketResult(t, secondResp.Body.Bytes())
	if second.IsTruncated {
		t.Fatal("expected second page to not be truncated")
	}
	if second.ContinuationToken != first.NextContinuationToken {
		t.Fatalf("ContinuationToken = %q, want %q", second.ContinuationToken, first.NextContinuationToken)
	}
	assertStringSlice(t, listResultKeys(second), []string{"c.txt"})
}

func TestListObjectsV2Delimiter(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "about.txt", "about")
	env.mustPut(t, "docs/a.txt", "a")
	env.mustPut(t, "docs/nested/b.txt", "b")
	env.mustPut(t, "images/a.jpg", "jpg")

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&delimiter=/", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	result := decodeListBucketResult(t, resp.Body.Bytes())
	assertStringSlice(t, listResultKeys(result), []string{"about.txt"})
	assertStringSlice(t, listResultPrefixes(result), []string{"docs/", "images/"})
	if result.KeyCount != 3 {
		t.Fatalf("KeyCount = %d, want 3", result.KeyCount)
	}
}

func TestListObjectsV2DelimiterPaginationSkipsCommonPrefixChildren(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "docs/a.txt", "a")
	env.mustPut(t, "docs/b.txt", "b")
	env.mustPut(t, "images/a.jpg", "jpg")

	firstResp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&delimiter=/&max-keys=1", "", nil)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", firstResp.Code, firstResp.Body.String())
	}
	first := decodeListBucketResult(t, firstResp.Body.Bytes())
	assertStringSlice(t, listResultPrefixes(first), []string{"docs/"})
	if !first.IsTruncated {
		t.Fatal("expected first delimiter page to be truncated")
	}

	secondResp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&delimiter=/&continuation-token="+first.NextContinuationToken, "", nil)
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", secondResp.Code, secondResp.Body.String())
	}
	second := decodeListBucketResult(t, secondResp.Body.Bytes())
	assertStringSlice(t, listResultPrefixes(second), []string{"images/"})
}

func TestListObjectsV2DelimiterPaginationSkipsLargeCommonPrefixChildren(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	for index := range 1001 {
		env.mustPut(t, fmt.Sprintf("docs/file-%04d.txt", index), "doc")
	}
	env.mustPut(t, "images/a.jpg", "jpg")

	firstResp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&delimiter=/&max-keys=1", "", nil)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", firstResp.Code, firstResp.Body.String())
	}
	first := decodeListBucketResult(t, firstResp.Body.Bytes())
	assertStringSlice(t, listResultPrefixes(first), []string{"docs/"})
	if !first.IsTruncated {
		t.Fatal("expected first delimiter page to be truncated")
	}

	secondResp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&delimiter=/&max-keys=1&continuation-token="+first.NextContinuationToken, "", nil)
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", secondResp.Code, secondResp.Body.String())
	}
	second := decodeListBucketResult(t, secondResp.Body.Bytes())
	assertStringSlice(t, listResultPrefixes(second), []string{"images/"})
}

func TestListObjectsV2ContinuationTokenDoesNotEchoStartAfter(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "a")
	env.mustPut(t, "b.txt", "b")
	env.mustPut(t, "c.txt", "c")

	firstResp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&max-keys=2", "", nil)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=%s", firstResp.Code, firstResp.Body.String())
	}
	first := decodeListBucketResult(t, firstResp.Body.Bytes())

	secondResp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&continuation-token="+first.NextContinuationToken, "", nil)
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", secondResp.Code, secondResp.Body.String())
	}
	second := decodeListBucketResult(t, secondResp.Body.Bytes())
	if second.StartAfter != "" {
		t.Fatalf("StartAfter = %q, want empty", second.StartAfter)
	}
}

func TestListObjectsV2StartAfterEchoesWhenRequested(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	firstPutResp := env.do(t, http.MethodPut, "/"+env.bucket+"/a%20file.txt", "a", nil)
	if firstPutResp.Code != http.StatusOK {
		t.Fatalf("first put status = %d, want 200; body=%s", firstPutResp.Code, firstPutResp.Body.String())
	}
	secondPutResp := env.do(t, http.MethodPut, "/"+env.bucket+"/b%20file.txt", "b", nil)
	if secondPutResp.Code != http.StatusOK {
		t.Fatalf("second put status = %d, want 200; body=%s", secondPutResp.Code, secondPutResp.Body.String())
	}

	resp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&start-after=a%20file.txt&encoding-type=url", "", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}

	result := decodeListBucketResult(t, resp.Body.Bytes())
	if result.StartAfter != "a%20file.txt" {
		t.Fatalf("StartAfter = %q, want a%%20file.txt", result.StartAfter)
	}
	assertStringSlice(t, listResultKeys(result), []string{"b%20file.txt"})
}

func TestListObjectsV2NoSuchBucket(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	resp := env.do(t, http.MethodGet, "/missing?list-type=2", "", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeNoSuchBucket)
}

func TestListObjectsV2OversizedContinuationTokenRejected(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "a")

	// Token whose base64 length exceeds maxContinuationTokenLen (1500).
	oversizedToken := strings.Repeat("A", maxContinuationTokenLen+1)
	resp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&continuation-token="+oversizedToken, "", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
}

func TestListObjectsV2OversizedDecodedKeyRejected(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "a")

	// Valid base64 of a byte slice larger than maxContinuationKeyLen (1024).
	bigKey := strings.Repeat("k", maxContinuationKeyLen+1)
	encodedToken := base64.RawURLEncoding.EncodeToString([]byte(bigKey))
	resp := env.do(t, http.MethodGet, "/"+env.bucket+"?list-type=2&continuation-token="+encodedToken, "", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
}

func TestDeleteObjects(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "a.txt", "a")
	env.mustPut(t, "b.txt", "b")
	env.mustPut(t, "c.txt", "c")

	// Standard S3 multi-object delete uses POST (boto3 / AWS CLI).
	body := `<Delete><Object><Key>a.txt</Key></Object><Object><Key>missing.txt</Key></Object></Delete>`
	resp := env.do(t, http.MethodPost, "/"+env.bucket+"?delete", body, deleteObjectsHeaders(body))
	if resp.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	var result deleteObjectsResult
	if err := xml.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal delete result: %v", err)
	}
	if len(result.Deleted) != 2 {
		t.Fatalf("len(deleted) = %d, want 2", len(result.Deleted))
	}
	if _, err := env.objects.GetByKey(context.Background(), env.bucket, "a.txt"); err != metadata.ErrObjectNotFound {
		t.Fatalf("deleted object lookup err = %v, want ErrObjectNotFound", err)
	}

	// DELETE ?delete remains accepted for compatibility.
	compatBody := `<Delete><Object><Key>c.txt</Key></Object></Delete>`
	compat := env.do(t, http.MethodDelete, "/"+env.bucket+"?delete", compatBody, deleteObjectsHeaders(compatBody))
	if compat.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200; body=%s", compat.Code, compat.Body.String())
	}

	quietBody := `<Delete><Quiet>true</Quiet><Object><Key>b.txt</Key></Object></Delete>`
	quiet := env.do(t, http.MethodPost, "/"+env.bucket+"?delete", quietBody, deleteObjectsHeaders(quietBody))
	if quiet.Code != http.StatusOK {
		t.Fatalf("quiet status = %d, want 200; body=%s", quiet.Code, quiet.Body.String())
	}
	if strings.Contains(quiet.Body.String(), "<Deleted>") {
		t.Fatalf("quiet response included Deleted entries: %s", quiet.Body.String())
	}
}

func TestDeleteObjectsInvalidRequests(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	missingDigest := env.do(t, http.MethodDelete, "/"+env.bucket+"?delete", `<Delete><Object><Key>a.txt</Key></Object></Delete>`, map[string]string{"Content-Type": "application/xml"})
	if missingDigest.Code != http.StatusBadRequest {
		t.Fatalf("missing digest status = %d, want 400; response=%s", missingDigest.Code, missingDigest.Body.String())
	}
	assertS3ErrorCode(t, missingDigest.Body.Bytes(), codeInvalidRequest)

	cases := []string{
		`<Delete>`,
		`<Delete></Delete>`,
	}
	for _, body := range cases {
		resp := env.do(t, http.MethodDelete, "/"+env.bucket+"?delete", body, deleteObjectsHeaders(body))
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, want 400; response=%s", body, resp.Code, resp.Body.String())
		}
		assertS3ErrorCode(t, resp.Body.Bytes(), codeMalformedXML)
	}

	var tooMany strings.Builder
	tooMany.WriteString("<Delete>")
	for i := 0; i < maxDeleteObjects+1; i++ {
		tooMany.WriteString(fmt.Sprintf("<Object><Key>key-%d</Key></Object>", i))
	}
	tooMany.WriteString("</Delete>")
	resp := env.do(t, http.MethodDelete, "/"+env.bucket+"?delete", tooMany.String(), deleteObjectsHeaders(tooMany.String()))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("too many status = %d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	assertS3ErrorCode(t, resp.Body.Bytes(), codeInvalidRequest)

	missingBucket := env.do(t, http.MethodDelete, "/missing?delete", `<Delete><Object><Key>a.txt</Key></Object></Delete>`, map[string]string{"Content-Type": "application/xml"})
	if missingBucket.Code != http.StatusNotFound {
		t.Fatalf("missing bucket status = %d, want 404; body=%s", missingBucket.Code, missingBucket.Body.String())
	}
}

func TestCopyObject(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	put := env.do(t, http.MethodPut, "/"+env.bucket+"/source%20file.txt", "copy me", map[string]string{"Content-Type": "text/plain"})
	if put.Code != http.StatusOK {
		t.Fatalf("put status = %d, want 200; body=%s", put.Code, put.Body.String())
	}

	resp := env.do(t, http.MethodPut, "/"+env.bucket+"/copied.txt", "", map[string]string{
		"x-amz-copy-source": "/" + env.bucket + "/source%20file.txt",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("copy status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	obj, err := env.objects.GetByKey(context.Background(), env.bucket, "copied.txt")
	if err != nil {
		t.Fatalf("get copied metadata: %v", err)
	}
	if obj.ContentType != "text/plain" || quoteETag(obj.ETag) != put.Header().Get("ETag") {
		t.Fatalf("copied metadata = %+v etag header=%q", obj, put.Header().Get("ETag"))
	}

	oldCopiedPath := obj.StoragePath
	overwrite := env.do(t, http.MethodPut, "/"+env.bucket+"/copied.txt", "", map[string]string{
		"x-amz-copy-source": "/" + env.bucket + "/source%20file.txt",
	})
	if overwrite.Code != http.StatusOK {
		t.Fatalf("overwrite copy status = %d, want 200; body=%s", overwrite.Code, overwrite.Body.String())
	}
	if _, err := env.storage.Open(context.Background(), oldCopiedPath); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("old copied backing file error = %v, want ErrNotFound", err)
	}

	replace := env.do(t, http.MethodPut, "/"+env.bucket+"/replaced.txt", "", map[string]string{
		"x-amz-copy-source":        env.bucket + "/source%20file.txt",
		"x-amz-metadata-directive": "REPLACE",
		"Content-Type":             "application/json",
	})
	if replace.Code != http.StatusOK {
		t.Fatalf("replace status = %d, want 200; body=%s", replace.Code, replace.Body.String())
	}
	replaced, err := env.objects.GetByKey(context.Background(), env.bucket, "replaced.txt")
	if err != nil {
		t.Fatalf("get replaced metadata: %v", err)
	}
	if replaced.ContentType != "application/json" {
		t.Fatalf("ContentType = %q, want application/json", replaced.ContentType)
	}
}

func TestSigV4CopyObjectRequiresSignedCopyHeaders(t *testing.T) {
	t.Parallel()

	env := newSigV4S3TestEnv(t)
	seedObject(t, env, env.bucket, "source.txt", "source")

	req := httptest.NewRequest(http.MethodPut, "/"+env.bucket+"/copied.txt", strings.NewReader(""))
	req.Host = "localhost:9000"
	req.Header.Set("x-amz-copy-source", "/"+env.bucket+"/source.txt")
	auth.SignRequest(req, env.sigv4.AccessKeyID, env.sigv4.SecretKey, "us-east-1", "s3", []string{"host", "x-amz-date"}, auth.EmptyStringHash)

	rr := httptest.NewRecorder()
	env.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	assertS3ErrorCode(t, rr.Body.Bytes(), codeAccessDenied)
}

func TestSigV4DeleteObjectsRequiresSignedDigestHeader(t *testing.T) {
	t.Parallel()

	env := newSigV4S3TestEnv(t)
	body := `<Delete><Object><Key>missing.txt</Key></Object></Delete>`
	req := httptest.NewRequest(http.MethodDelete, "/"+env.bucket+"?delete", strings.NewReader(body))
	req.Host = "localhost:9000"
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Content-MD5", base64MD5(body))
	auth.SignRequest(req, env.sigv4.AccessKeyID, env.sigv4.SecretKey, "us-east-1", "s3", []string{"host", "x-amz-date"}, auth.EmptyStringHash)

	rr := httptest.NewRecorder()
	env.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	assertS3ErrorCode(t, rr.Body.Bytes(), codeAccessDenied)
}

func TestCopyObjectErrors(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "source.txt", "source")

	cases := []struct {
		name   string
		path   string
		source string
		status int
		code   string
	}{
		{"missing destination bucket", "/missing/dest.txt", env.bucket + "/source.txt", http.StatusNotFound, codeNoSuchBucket},
		{"missing source bucket", "/" + env.bucket + "/dest.txt", "missing/source.txt", http.StatusNotFound, codeNoSuchBucket},
		{"missing source key", "/" + env.bucket + "/dest.txt", env.bucket + "/missing.txt", http.StatusNotFound, codeNoSuchKey},
		{"version id unsupported", "/" + env.bucket + "/dest.txt", env.bucket + "/source.txt?versionId=1", http.StatusNotImplemented, codeNotImplemented},
		{"bad directive", "/" + env.bucket + "/dest.txt", env.bucket + "/source.txt", http.StatusBadRequest, codeInvalidRequest},
	}
	for _, tc := range cases {
		headers := map[string]string{"x-amz-copy-source": tc.source}
		if tc.name == "bad directive" {
			headers["x-amz-metadata-directive"] = "BAD"
		}
		resp := env.do(t, http.MethodPut, tc.path, "", headers)
		if resp.Code != tc.status {
			t.Fatalf("%s status = %d, want %d; body=%s", tc.name, resp.Code, tc.status, resp.Body.String())
		}
		assertS3ErrorCode(t, resp.Body.Bytes(), tc.code)
	}
}

func TestNotImplementedSubresources(t *testing.T) {
	t.Parallel()

	env := newObjectTestEnv(t)
	env.mustPut(t, "object.txt", "body")
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/" + env.bucket + "?versions"},
		{http.MethodGet, "/" + env.bucket + "?acl"},
		{http.MethodPut, "/" + env.bucket + "?acl"},
		{http.MethodGet, "/" + env.bucket + "/object.txt?acl"},
		{http.MethodPut, "/" + env.bucket + "/object.txt?acl"},
		{http.MethodGet, "/" + env.bucket + "?cors"},
		{http.MethodPut, "/" + env.bucket + "?cors"},
		{http.MethodDelete, "/" + env.bucket + "?cors"},
		{http.MethodGet, "/" + env.bucket + "?policy"},
		{http.MethodPut, "/" + env.bucket + "?policy"},
		{http.MethodDelete, "/" + env.bucket + "?policy"},
		{http.MethodGet, "/" + env.bucket + "?uploads"},
	}
	for _, tc := range cases {
		resp := env.do(t, tc.method, tc.path, "", nil)
		if resp.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, want 501; body=%s", tc.method, tc.path, resp.Code, resp.Body.String())
		}
		assertS3ErrorCode(t, resp.Body.Bytes(), codeNotImplemented)
	}
}

func TestPresignedQueryAuthAgainstS3Routes(t *testing.T) {
	t.Parallel()

	env := newSigV4S3TestEnv(t)

	for _, tc := range []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/", "", http.StatusOK},
		{http.MethodGet, "/" + env.bucket + "?list-type=2", "", http.StatusOK},
		{http.MethodPut, "/" + env.bucket + "/presigned.txt", "body", http.StatusOK},
		{http.MethodGet, "/" + env.bucket + "/presigned.txt", "", http.StatusOK},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Host = "localhost:9000"
		auth.PresignRequest(req, env.sigv4.AccessKeyID, env.sigv4.SecretKey, "us-east-1", "s3", time.Hour, time.Now().UTC())
		rr := httptest.NewRecorder()
		env.router.ServeHTTP(rr, req)
		if rr.Code != tc.status {
			t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.path, rr.Code, tc.status, rr.Body.String())
		}
	}
}

func decodeListBucketResult(t *testing.T, body []byte) listBucketResult {
	t.Helper()
	var result listBucketResult
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal list bucket result: %v; body=%s", err, string(body))
	}
	return result
}

func decodeListBucketV1Result(t *testing.T, body []byte) listBucketV1Result {
	t.Helper()
	var result listBucketV1Result
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal list bucket v1 result: %v; body=%s", err, string(body))
	}
	return result
}

func listV1ResultKeys(result listBucketV1Result) []string {
	keys := make([]string, 0, len(result.Contents))
	for _, object := range result.Contents {
		keys = append(keys, object.Key)
	}
	return keys
}

func listV1ResultPrefixes(result listBucketV1Result) []string {
	prefixes := make([]string, 0, len(result.CommonPrefixes))
	for _, commonPrefix := range result.CommonPrefixes {
		prefixes = append(prefixes, commonPrefix.Prefix)
	}
	return prefixes
}

func listResultKeys(result listBucketResult) []string {
	keys := make([]string, 0, len(result.Contents))
	for _, object := range result.Contents {
		keys = append(keys, object.Key)
	}
	return keys
}

func listResultPrefixes(result listBucketResult) []string {
	prefixes := make([]string, 0, len(result.CommonPrefixes))
	for _, commonPrefix := range result.CommonPrefixes {
		prefixes = append(prefixes, commonPrefix.Prefix)
	}
	return prefixes
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d; got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q; got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}

func createBucketConfigXML(locationConstraint string) string {
	return fmt.Sprintf(`<CreateBucketConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><LocationConstraint>%s</LocationConstraint></CreateBucketConfiguration>`, locationConstraint)
}

func deleteObjectsHeaders(body string) map[string]string {
	return map[string]string{
		"Content-Type": "application/xml",
		"Content-MD5":  base64MD5(body),
	}
}

func seedObject(t *testing.T, env objectTestEnv, bucketName, key, body string) {
	t.Helper()
	storagePath, size, err := env.storage.Write(context.Background(), bucketName, key, strings.NewReader(body))
	if err != nil {
		t.Fatalf("write object data: %v", err)
	}
	now := time.Now().UTC()
	if err := env.objects.Create(context.Background(), &metadata.Object{
		ID:          "seed-" + key,
		BucketName:  bucketName,
		Key:         key,
		Size:        size,
		ETag:        strings.Trim(quotedMD5(body), `"`),
		ContentType: "application/octet-stream",
		StoragePath: storagePath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create object metadata: %v", err)
	}
}

type staticAuthenticator struct {
	principal auth.Principal
}

func (a staticAuthenticator) Authenticate(_ *http.Request) (auth.Principal, error) {
	return a.principal, nil
}

func newScopedS3TestEnv(t *testing.T, principal auth.Principal) objectTestEnv {
	t.Helper()

	db, err := metadata.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	userRepo := metadata.NewUserRepository(db)
	now := time.Now().UTC()
	for _, user := range []*metadata.User{
		{ID: "member-user", DisplayName: "Member User", AccessKeyID: "member", SecretHash: "hash", Role: "member", IsActive: true, CreatedAt: now, UpdatedAt: now},
		{ID: "other-user", DisplayName: "Other User", AccessKeyID: "other", SecretHash: "hash", Role: "member", IsActive: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := userRepo.Create(context.Background(), user); err != nil {
			t.Fatalf("create user %s: %v", user.ID, err)
		}
	}

	bucketRepo := metadata.NewBucketRepository(db)
	for _, bucket := range []*metadata.Bucket{
		{Name: "member-bucket", OwnerID: "member-user", CreatedAt: now},
		{Name: "other-bucket", OwnerID: "other-user", CreatedAt: now.Add(time.Second)},
	} {
		if err := bucketRepo.Create(context.Background(), bucket); err != nil {
			t.Fatalf("create bucket %s: %v", bucket.Name, err)
		}
	}

	disk, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	objectRepo := metadata.NewObjectRepository(db)
	grantRepo := metadata.NewGrantRepository(db)
	handlers := &ObjectHandlers{
		Users:   userRepo,
		Buckets: bucketRepo,
		Objects: objectRepo,
		Grants:  grantRepo,
		Authz:   NewAuthzEvaluator(grantRepo),
		Storage: disk,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	router := httpapi.NewRouter(config.Default(), nil, func(r chi.Router) {
		r.Group(func(protected chi.Router) {
			protected.Use(auth.RequireAuthentication(staticAuthenticator{principal: principal}, func(w http.ResponseWriter, r *http.Request, err error) {
				WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
			}))
			RegisterBucketRoutes(protected, handlers)
			RegisterObjectRoutes(protected, handlers)
		})
	})

	return objectTestEnv{
		router:  router,
		users:   userRepo,
		buckets: bucketRepo,
		objects: objectRepo,
		grants:  grantRepo,
		storage: disk,
		bucket:  "member-bucket",
		dataDir: t.TempDir(),
		userID:  principal.UserID,
	}
}

func newSigV4S3TestEnv(t *testing.T) objectTestEnv {
	t.Helper()

	db, err := metadata.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	userRepo := metadata.NewUserRepository(db)
	_, sigv4, user, err := auth.CreateBearerToken(context.Background(), userRepo, "SigV4 User", "admin")
	if err != nil {
		t.Fatalf("create bearer token: %v", err)
	}

	bucketRepo := metadata.NewBucketRepository(db)
	bucketName := "sigv4-bucket"
	if err := bucketRepo.Create(context.Background(), &metadata.Bucket{
		Name:      bucketName,
		OwnerID:   user.ID,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	disk, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	objectRepo := metadata.NewObjectRepository(db)
	grantRepo := metadata.NewGrantRepository(db)
	handlers := &ObjectHandlers{
		Users:   userRepo,
		Buckets: bucketRepo,
		Objects: objectRepo,
		Grants:  grantRepo,
		Authz:   NewAuthzEvaluator(grantRepo),
		Storage: disk,
	}
	authChain := &auth.ChainAuthenticator{
		Authenticators: []auth.Authenticator{
			&auth.SigV4Authenticator{Repo: metadata.NewSigV4UserRepository(db)},
		},
	}
	router := httpapi.NewRouter(config.Default(), nil, func(r chi.Router) {
		r.Group(func(protected chi.Router) {
			protected.Use(auth.RequireAuthentication(authChain, func(w http.ResponseWriter, r *http.Request, err error) {
				WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
			}))
			RegisterBucketRoutes(protected, handlers)
			RegisterObjectRoutes(protected, handlers)
		})
	})

	return objectTestEnv{
		router:  router,
		users:   userRepo,
		buckets: bucketRepo,
		objects: objectRepo,
		storage: disk,
		sigv4:   sigv4,
		bucket:  bucketName,
		userID:  user.ID,
	}
}
