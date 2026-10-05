package management_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type shareLinkJSON struct {
	Code                       string  `json:"code"`
	URL                        string  `json:"url"`
	Bucket                     string  `json:"bucket"`
	Key                        string  `json:"key"`
	ResponseContentDisposition string  `json:"response_content_disposition"`
	CreatedBy                  string  `json:"created_by"`
	ExpiresAt                  *string `json:"expires_at"`
	CreatedAt                  string  `json:"created_at"`
}

const shareLinksPath = "/api/management/share-links"

func (e managementTestEnv) createShareLink(t *testing.T, body string, wantStatus int) shareLinkJSON {
	t.Helper()
	resp := e.do(t, http.MethodPost, shareLinksPath, e.adminToken, strings.NewReader(body))
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("create share link status = %d, want %d; body=%s", resp.StatusCode, wantStatus, readBody(t, resp))
	}
	var link shareLinkJSON
	if wantStatus == http.StatusCreated {
		decodeResponse(t, resp, &link)
	}
	return link
}

func TestManagementCreateShareLinkGeneratesRandomCode(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	link := env.createShareLink(t, `{"bucket":"photos","key":"2026/image.jpg"}`, http.StatusCreated)

	if len(link.Code) != 10 {
		t.Fatalf("code = %q, want 10 characters", link.Code)
	}
	if link.URL != "http://example.com/s/"+link.Code {
		t.Fatalf("url = %q", link.URL)
	}
	if link.Bucket != "photos" || link.Key != "2026/image.jpg" || link.CreatedBy != env.adminUserID {
		t.Fatalf("unexpected link: %+v", link)
	}
	if link.ExpiresAt != nil {
		t.Fatalf("expires_at = %v, want null", *link.ExpiresAt)
	}

	second := env.createShareLink(t, `{"bucket":"photos","key":"2026/image.jpg"}`, http.StatusCreated)
	if second.Code == link.Code {
		t.Fatal("random codes must differ between links")
	}
}

func TestManagementCreateShareLinkWithAliasAndExpiry(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	before := time.Now().UTC().Truncate(time.Second)
	link := env.createShareLink(t, `{"bucket":"photos","key":"2026/image.jpg","alias":"trip-2026","expires_in_seconds":3600,"response_content_disposition":"inline; filename=\"image.jpg\""}`, http.StatusCreated)

	if link.Code != "trip-2026" || !strings.HasSuffix(link.URL, "/s/trip-2026") {
		t.Fatalf("code = %q url = %q", link.Code, link.URL)
	}
	if link.ResponseContentDisposition != `inline; filename="image.jpg"` {
		t.Fatalf("disposition = %q", link.ResponseContentDisposition)
	}
	if link.ExpiresAt == nil {
		t.Fatal("expires_at = null, want a timestamp")
	}
	expiresAt, err := time.Parse(time.RFC3339, *link.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	if expiresAt.Before(before.Add(time.Hour)) || expiresAt.After(before.Add(time.Hour+time.Minute)) {
		t.Fatalf("expires_at = %v, want about one hour from %v", expiresAt, before)
	}

	env.createShareLink(t, `{"bucket":"photos","key":"2026/raw/a.nef","alias":"trip-2026"}`, http.StatusConflict)
}

func TestManagementCreateShareLinkValidation(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"invalid json", `{`, http.StatusBadRequest},
		{"missing bucket", `{"key":"2026/image.jpg"}`, http.StatusBadRequest},
		{"missing key", `{"bucket":"photos"}`, http.StatusBadRequest},
		{"invalid alias", `{"bucket":"photos","key":"2026/image.jpg","alias":"a/b"}`, http.StatusBadRequest},
		{"zero ttl", `{"bucket":"photos","key":"2026/image.jpg","expires_in_seconds":0}`, http.StatusBadRequest},
		{"huge ttl", `{"bucket":"photos","key":"2026/image.jpg","expires_in_seconds":9223372036854775807}`, http.StatusBadRequest},
		{"bad disposition", `{"bucket":"photos","key":"2026/image.jpg","response_content_disposition":"attachment\r\nX-Injected: true"}`, http.StatusBadRequest},
		{"missing bucket row", `{"bucket":"nope","key":"2026/image.jpg"}`, http.StatusNotFound},
		{"missing object", `{"bucket":"photos","key":"missing.jpg"}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env.createShareLink(t, tt.body, tt.status)
		})
	}
}

func TestManagementListAndDeleteShareLinks(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	env.createShareLink(t, `{"bucket":"photos","key":"2026/image.jpg","alias":"photo-link"}`, http.StatusCreated)
	env.createShareLink(t, `{"bucket":"docs","key":"readme.txt","alias":"doc-link"}`, http.StatusCreated)

	listCodes := func(query string) []string {
		resp := env.do(t, http.MethodGet, shareLinksPath+query, env.adminToken, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list status = %d", resp.StatusCode)
		}
		var payload struct {
			ShareLinks []shareLinkJSON `json:"share_links"`
		}
		decodeResponse(t, resp, &payload)
		codes := make([]string, 0, len(payload.ShareLinks))
		for _, link := range payload.ShareLinks {
			codes = append(codes, link.Code)
		}
		return codes
	}

	if got := listCodes(""); len(got) != 2 {
		t.Fatalf("all links = %v, want 2", got)
	}
	if got := listCodes("?bucket=" + url.QueryEscape("docs")); len(got) != 1 || got[0] != "doc-link" {
		t.Fatalf("docs links = %v, want [doc-link]", got)
	}

	resp := env.do(t, http.MethodDelete, shareLinksPath+"/doc-link", env.adminToken, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", resp.StatusCode)
	}
	resp = env.do(t, http.MethodDelete, shareLinksPath+"/doc-link", env.adminToken, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", resp.StatusCode)
	}
	if got := listCodes(""); len(got) != 1 || got[0] != "photo-link" {
		t.Fatalf("links after delete = %v, want [photo-link]", got)
	}
}

func TestManagementShareLinksRequireAdmin(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		resp := env.do(t, method, shareLinksPath, env.memberToken, strings.NewReader(`{"bucket":"photos","key":"2026/image.jpg"}`))
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("member %s status = %d, want 403", method, resp.StatusCode)
		}
	}
}

func TestManagementShareLinkServesAndRevokes(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	seedStoredObject(t, env, "photos", "cdn/clip.txt", "shared body")
	link := env.createShareLink(t, `{"bucket":"photos","key":"cdn/clip.txt"}`, http.StatusCreated)

	shareURL, err := url.Parse(link.URL)
	if err != nil {
		t.Fatalf("parse share url: %v", err)
	}
	resp := env.do(t, http.MethodGet, shareURL.Path, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share link status = %d, want 200", resp.StatusCode)
	}
	if got := string(readBody(t, resp)); got != "shared body" {
		t.Fatalf("share link body = %q", got)
	}

	revoke := env.do(t, http.MethodDelete, shareLinksPath+"/"+link.Code, env.adminToken, nil)
	revoke.Body.Close()
	resp = env.do(t, http.MethodGet, shareURL.Path, "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked share link status = %d, want 404", resp.StatusCode)
	}
}

func TestManagementShareLinkDatabaseFailureLogsCause(t *testing.T) {
	t.Parallel()

	env := newManagementTestEnv(t)
	if _, err := env.db.Exec(`DROP TABLE share_links`); err != nil {
		t.Fatalf("drop share links: %v", err)
	}
	resp := env.do(t, http.MethodGet, shareLinksPath, env.adminToken, nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("list status = %d, want 500", resp.StatusCode)
	}
	if body := string(readBody(t, resp)); strings.Contains(body, "no such table") {
		t.Fatalf("response leaks internal error: %s", body)
	}
	if logs := env.logs.String(); !strings.Contains(logs, "no such table") || !strings.Contains(logs, "path="+shareLinksPath) {
		t.Fatalf("missing database cause or request path in log: %s", logs)
	}
}
