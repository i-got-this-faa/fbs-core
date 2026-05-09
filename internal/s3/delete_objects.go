package s3

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
)

const maxDeleteObjects = 1000

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
}

type deletedObjectEntry struct {
	Key string `xml:"Key"`
}

func (h *ObjectHandlers) DeleteObjects(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	if !h.ensureBucket(w, r, bucketName) {
		return
	}

	req, err := parseDeleteObjectsRequest(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		code := codeMalformedXML
		message := messageMalformedXML
		if errors.Is(err, errTooManyDeleteObjects) {
			code = codeInvalidRequest
			message = messageInvalidRequest
		}
		WriteS3Error(w, r, status, code, message)
		return
	}

	result := deleteObjectsResult{
		Xmlns:   "http://s3.amazonaws.com/doc/2006-03-01/",
		Deleted: make([]deletedObjectEntry, 0, len(req.Objects)),
	}
	for _, requestedObject := range req.Objects {
		key := requestedObject.Key
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

func parseDeleteObjectsRequest(body io.ReadCloser) (deleteObjectsRequest, error) {
	defer body.Close()

	payload, err := io.ReadAll(io.LimitReader(body, 2<<20))
	if err != nil {
		return deleteObjectsRequest{}, err
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return deleteObjectsRequest{}, errors.New("empty delete request")
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
