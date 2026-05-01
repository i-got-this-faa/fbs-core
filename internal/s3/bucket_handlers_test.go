package s3

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"testing"
)

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

func decodeListBucketResult(t *testing.T, body []byte) listBucketResult {
	t.Helper()
	var result listBucketResult
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal list bucket result: %v; body=%s", err, string(body))
	}
	return result
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
