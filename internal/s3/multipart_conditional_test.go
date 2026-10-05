package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/storage"
)

func TestCompleteMultipartUploadConditionalFailurePreservesUpload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		header     string
		value      string
		existing   bool
		wantStatus int
		wantCode   string
	}{
		{"if-none-match existing key", "If-None-Match", "*", true, http.StatusPreconditionFailed, codePreconditionFailed},
		{"if-match mismatched etag", "If-Match", `"different"`, true, http.StatusPreconditionFailed, codePreconditionFailed},
		{"if-match missing key", "If-Match", `"different"`, false, http.StatusNotFound, codeNoSuchKey},
	}
	for _, tt := range tests {
		for _, followup := range []string{"abort", "complete"} {
			t.Run(tt.name+"/"+followup, func(t *testing.T) {
				t.Parallel()

				env := newObjectTestEnv(t)
				const key = "conditional.txt"
				if tt.existing {
					env.mustPut(t, key, "original")
				}
				uploadID := env.mustCreateMultipartUpload(t, key)
				etag := env.mustUploadPart(t, uploadID, key, 1, "uploaded")
				path := fmt.Sprintf("/%s/%s?uploadId=%s", env.bucket, key, uploadID)
				completeXML := fmt.Sprintf(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>`, etag)
				ctx := context.Background()
				parts, err := env.multipartUploads.ListParts(ctx, uploadID)
				if err != nil || len(parts) != 1 {
					t.Fatalf("parts before completion = %v, err = %v", parts, err)
				}
				partPath := parts[0].StoragePath

				for range 2 {
					resp := env.do(t, http.MethodPost, path, completeXML, map[string]string{tt.header: tt.value})
					if resp.Code != tt.wantStatus {
						t.Fatalf("conditional completion status = %d, want %d; body=%s", resp.Code, tt.wantStatus, resp.Body.String())
					}
					assertS3ErrorCode(t, resp.Body.Bytes(), tt.wantCode)
					upload, err := env.multipartUploads.GetByID(ctx, uploadID)
					if err != nil {
						t.Fatalf("load upload after failed completion: %v", err)
					}
					if upload.Status != metadata.MultipartUploadStatusActive {
						t.Fatalf("upload status = %q, want active", upload.Status)
					}
					parts, err := env.multipartUploads.ListParts(ctx, uploadID)
					if err != nil || len(parts) != 1 || parts[0].StoragePath != partPath {
						t.Fatalf("parts after failed completion = %v, err = %v", parts, err)
					}
					part, err := env.storage.Read(ctx, partPath)
					if err != nil {
						t.Fatalf("read retained part: %v", err)
					}
					body, err := io.ReadAll(part)
					_ = part.Close()
					if err != nil || string(body) != "uploaded" {
						t.Fatalf("retained part = %q, err = %v", body, err)
					}
					resp = env.do(t, http.MethodGet, "/"+env.bucket+"/"+key, "", nil)
					wantFiles := 0
					if tt.existing {
						if resp.Code != http.StatusOK || resp.Body.String() != "original" {
							t.Fatalf("original object changed: status=%d body=%q", resp.Code, resp.Body.String())
						}
						wantFiles = 1
					} else if resp.Code != http.StatusNotFound {
						t.Fatalf("failed completion created an object: status=%d", resp.Code)
					}
					files, err := os.ReadDir(filepath.Join(env.dataDir, env.bucket))
					if err != nil && !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("read bucket storage: %v", err)
					}
					if len(files) != wantFiles {
						t.Fatalf("bucket has %d backing files, want %d; failed assembly was not cleaned up", len(files), wantFiles)
					}
				}

				if followup == "abort" {
					resp := env.do(t, http.MethodDelete, path, "", nil)
					if resp.Code != http.StatusNoContent {
						t.Fatalf("abort status = %d, want 204; body=%s", resp.Code, resp.Body.String())
					}
				} else {
					// A failed completion must still allow replacing a part.
					etag = env.mustUploadPart(t, uploadID, key, 1, "replacement")
					if _, err := env.storage.Read(ctx, partPath); !errors.Is(err, storage.ErrNotFound) {
						t.Fatalf("replaced part remains: %v", err)
					}
					if tt.header == "If-None-Match" {
						resp := env.do(t, http.MethodDelete, "/"+env.bucket+"/"+key, "", nil)
						if resp.Code != http.StatusNoContent {
							t.Fatalf("delete existing object: status=%d", resp.Code)
						}
					} else if !tt.existing {
						env.mustPut(t, key, "original")
					}
					headers := map[string]string{tt.header: "*"}
					if tt.header == "If-Match" {
						headers[tt.header] = quotedMD5("original")
					}
					completeXML = fmt.Sprintf(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>`, etag)
					resp := env.do(t, http.MethodPost, path, completeXML, headers)
					if resp.Code != http.StatusOK {
						t.Fatalf("retry completion status = %d, want 200; body=%s", resp.Code, resp.Body.String())
					}
					resp = env.do(t, http.MethodGet, "/"+env.bucket+"/"+key, "", nil)
					if resp.Code != http.StatusOK || resp.Body.String() != "replacement" {
						t.Fatalf("completed object status=%d body=%q", resp.Code, resp.Body.String())
					}
				}
				if _, err := env.multipartUploads.GetByID(ctx, uploadID); !errors.Is(err, metadata.ErrMultipartUploadNotFound) {
					t.Fatalf("finished upload lookup = %v, want not found", err)
				}
				if _, err := os.Stat(filepath.Join(env.dataDir, ".tmp", "multipart", uploadID)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("finished upload part directory remains: %v", err)
				}
			})
		}
	}
}
