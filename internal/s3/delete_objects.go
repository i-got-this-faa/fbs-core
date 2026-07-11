package s3

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/authz"
)

const maxDeleteObjects = 1000
const unsignedPayloadHash = "UNSIGNED-PAYLOAD"

type deleteObjectsRequest struct {
	XMLName xml.Name              `xml:"Delete"`
	Quiet   bool                  `xml:"Quiet"`
	Objects []deleteObjectRequest `xml:"Object"`
}

type deleteObjectRequest struct {
	Key string `xml:"Key"`
}

type deleteObjectsResult struct {
	XMLName xml.Name             `xml:"DeleteResult"`
	Xmlns   string               `xml:"xmlns,attr"`
	Deleted []deletedObjectEntry `xml:"Deleted,omitempty"`
	Errors  []deleteObjectError  `xml:"Error,omitempty"`
}

type deletedObjectEntry struct {
	Key string `xml:"Key"`
}

type deleteObjectError struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func (h *ObjectHandlers) DeleteObjects(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	bucket, ok := h.loadBucket(w, r, bucketName)
	if !ok {
		return
	}
	// Deny callers with no relationship to the bucket before per-key auth.
	// Without this, strangers get 200 + AccessDenied errors and can tell the
	// bucket exists more cheaply than via other ops' 403 shape consistency.
	if !h.authorizeBucketRelationship(w, r, bucket) {
		return
	}

	req, err := parseDeleteObjectsRequest(r)
	if err != nil {
		status := http.StatusBadRequest
		code := codeMalformedXML
		message := messageMalformedXML
		switch {
		case errors.Is(err, errUnsignedDeleteDigest):
			status = http.StatusForbidden
			code = codeAccessDenied
			message = messageAccessDenied
		case errors.Is(err, errTooManyDeleteObjects), errors.Is(err, errMissingDeleteDigest):
			code = codeInvalidRequest
			message = messageInvalidRequest
		case errors.Is(err, errInvalidDigest):
			code = codeInvalidDigest
			message = messageInvalidDigest
		case errors.Is(err, errChecksumMismatch):
			code = codeBadDigest
			message = messageBadDigest
		}
		WriteS3Error(w, r, status, code, message)
		return
	}

	result := deleteObjectsResult{
		Xmlns:   "http://s3.amazonaws.com/doc/2006-03-01/",
		Deleted: make([]deletedObjectEntry, 0, len(req.Objects)),
		Errors:  make([]deleteObjectError, 0),
	}
	for _, requestedObject := range req.Objects {
		key := requestedObject.Key
		allowed, authErr := h.authorizeQuiet(r, authz.ActionDeleteObject, bucketName, key, bucket)
		if authErr != nil {
			h.logError("authorize delete object in batch", authErr, bucketName, key, "")
			WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
			return
		}
		if !allowed {
			// Quiet mode suppresses <Deleted> entries only; per-key <Error>
			// entries must still be returned (AWS S3 multi-delete contract).
			result.Errors = append(result.Errors, deleteObjectError{
				Key:     key,
				Code:    codeAccessDenied,
				Message: messageAccessDenied,
			})
			continue
		}

		obj, existed, err := h.deleteObject(r, bucketName, key)
		if err != nil {
			h.logError("delete object in batch", err, bucketName, key, "")
			WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
			return
		}
		if existed {
			h.recordActivity(r, "delete_object", bucketName, key, obj.Size, obj.ETag)
		}
		if !req.Quiet {
			result.Deleted = append(result.Deleted, deletedObjectEntry{Key: key})
		}
	}
	h.recordActivity(r, "delete_objects", bucketName, "", int64(len(req.Objects)), "")

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(result)
}

var errTooManyDeleteObjects = errors.New("too many delete objects")
var errMissingDeleteDigest = errors.New("missing delete object digest")
var errUnsignedDeleteDigest = errors.New("unsigned delete object digest")

func parseDeleteObjectsRequest(r *http.Request) (deleteObjectsRequest, error) {
	defer r.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return deleteObjectsRequest{}, err
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return deleteObjectsRequest{}, errors.New("empty delete request")
	}
	if err := verifyDeleteObjectsDigest(r, payload); err != nil {
		return deleteObjectsRequest{}, err
	}

	var req deleteObjectsRequest
	if err := xml.Unmarshal(payload, &req); err != nil {
		return deleteObjectsRequest{}, err
	}
	if req.XMLName.Local != "Delete" || len(req.Objects) == 0 {
		return deleteObjectsRequest{}, errors.New("invalid delete request")
	}
	if len(req.Objects) > maxDeleteObjects {
		return deleteObjectsRequest{}, errTooManyDeleteObjects
	}
	for _, object := range req.Objects {
		if object.Key == "" {
			return deleteObjectsRequest{}, errors.New("missing delete object key")
		}
	}

	return req, nil
}

func verifyDeleteObjectsDigest(r *http.Request, payload []byte) error {
	if value := r.Header.Get("Content-MD5"); value != "" {
		if !requestHasSignedHeader(r, "content-md5") {
			return errUnsignedDeleteDigest
		}
		expected, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(expected) != md5.Size {
			return errInvalidDigest
		}
		actual := md5.Sum(payload)
		if !bytes.Equal(expected, actual[:]) {
			return errChecksumMismatch
		}
		return nil
	}

	if value := r.Header.Get("X-Amz-Content-SHA256"); value != "" && value != unsignedPayloadHash {
		if !requestHasSignedHeader(r, "x-amz-content-sha256") {
			return errUnsignedDeleteDigest
		}
		expected, err := hex.DecodeString(value)
		if err != nil || len(expected) != sha256.Size {
			return errInvalidDigest
		}
		actual := sha256.Sum256(payload)
		if !bytes.Equal(expected, actual[:]) {
			return errChecksumMismatch
		}
		return nil
	}

	return errMissingDeleteDigest
}
