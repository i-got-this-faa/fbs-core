package s3

import "net/http"

func (h *ObjectHandlers) DispatchBucketGet(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	switch {
	case query.Has("acl"), query.Has("cors"), query.Has("policy"):
		h.NotImplemented(w, r)
	case query.Has("versions"):
		h.ListObjectVersions(w, r)
	case query.Has("uploads"):
		h.ListMultipartUploads(w, r)
	case query.Has("location"):
		h.GetBucketLocation(w, r)
	case query.Get("list-type") == "2":
		h.ListObjectsV2(w, r)
	case query.Get("list-type") == "" || query.Get("list-type") == "1":
		h.ListObjectsV1(w, r)
	default:
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
	}
}

func (h *ObjectHandlers) DispatchBucketPut(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("acl") || query.Has("cors") || query.Has("policy") || query.Has("uploads") || query.Has("uploadId") {
		h.NotImplemented(w, r)
		return
	}
	h.CreateBucket(w, r)
}

func (h *ObjectHandlers) DispatchBucketPost(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	switch {
	case query.Has("delete"):
		// AWS DeleteObjects: POST /{bucket}?delete
		h.DeleteObjects(w, r)
	case query.Has("acl"), query.Has("cors"), query.Has("policy"), query.Has("uploads"), query.Has("uploadId"), query.Has("lifecycle"), query.Has("versioning"):
		h.NotImplemented(w, r)
	default:
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
	}
}

func (h *ObjectHandlers) DispatchBucketDelete(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	switch {
	case query.Has("cors"), query.Has("policy"), query.Has("uploads"), query.Has("uploadId"):
		h.NotImplemented(w, r)
	case query.Has("delete"):
		// Non-standard verb: fbs-web sends DeleteObjects as DELETE /{bucket}?delete.
		h.DeleteObjects(w, r)
	default:
		h.DeleteBucket(w, r)
	}
}

func (h *ObjectHandlers) DispatchObjectGet(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("uploadId") {
		h.ListParts(w, r)
		return
	}
	if query.Has("attributes") {
		h.GetObjectAttributes(w, r)
		return
	}
	if query.Has("acl") || query.Has("uploads") {
		h.NotImplemented(w, r)
		return
	}
	h.GetObject(w, r)
}

func (h *ObjectHandlers) DispatchObjectPut(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("acl") || query.Has("uploads") {
		h.NotImplemented(w, r)
		return
	}
	if query.Has("uploadId") || query.Has("partNumber") {
		h.DispatchPut(w, r)
		return
	}
	if r.Header.Get("x-amz-copy-source") != "" {
		h.CopyObject(w, r)
		return
	}
	h.PutObject(w, r)
}

func (h *ObjectHandlers) DispatchObjectDelete(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("uploads") {
		h.NotImplemented(w, r)
		return
	}
	if query.Has("uploadId") {
		h.DispatchDelete(w, r)
		return
	}
	h.DeleteObject(w, r)
}

// DispatchPut routes PUT requests to either PutObject or UploadPart.
func (h *ObjectHandlers) DispatchPut(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hasUploadID := q.Has("uploadId")
	hasPartNumber := q.Has("partNumber")
	if hasUploadID && hasPartNumber && q.Get("uploadId") != "" && q.Get("partNumber") != "" {
		if r.Header.Get("x-amz-copy-source") != "" {
			h.UploadPartCopy(w, r)
			return
		}
		h.UploadPart(w, r)
		return
	}
	if hasUploadID || hasPartNumber {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if r.Header.Get("x-amz-copy-source") != "" {
		h.CopyObject(w, r)
		return
	}
	h.PutObject(w, r)
}

// DispatchPost routes POST requests to either CreateMultipartUpload or CompleteMultipartUpload.
func (h *ObjectHandlers) DispatchPost(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Has("uploads") {
		h.CreateMultipartUpload(w, r)
		return
	}
	if q.Get("uploadId") != "" {
		h.CompleteMultipartUpload(w, r)
		return
	}
	WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
}

// DispatchDelete routes DELETE requests to either DeleteObject or AbortMultipartUpload.
func (h *ObjectHandlers) DispatchDelete(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Has("uploadId") {
		if q.Get("uploadId") == "" {
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
			return
		}
		h.AbortMultipartUpload(w, r)
		return
	}
	h.DeleteObject(w, r)
}
