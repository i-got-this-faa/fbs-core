package s3

import "net/http"

func (h *ObjectHandlers) DispatchBucketGet(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	switch {
	case query.Has("versions"), query.Has("acl"), query.Has("cors"), query.Has("policy"), query.Has("uploads"), query.Has("uploadId"):
		h.NotImplemented(w, r)
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
		// Non-standard verb; kept for compatibility with clients that send DELETE.
		h.DeleteObjects(w, r)
	default:
		h.DeleteBucket(w, r)
	}
}

func (h *ObjectHandlers) DispatchObjectGet(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("acl") || query.Has("uploadId") || query.Has("uploads") {
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
