package s3

import (
	"net/http"

	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/s3compat"
)

func (h *ObjectHandlers) HeadBucket(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	if bucketName == "" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	w.Header().Set("x-amz-bucket-region", s3compat.Region)
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionListBucket, "", "") {
		return
	}

	w.WriteHeader(http.StatusOK)
}
