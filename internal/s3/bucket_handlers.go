package s3

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/authz"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

const (
	defaultListMaxKeys = 1000
	maxListKeys        = 1000

	// maxContinuationTokenLen is the maximum accepted length of a base64-encoded
	// continuation token. S3 keys are at most 1024 bytes; base64 overhead is ~4/3,
	// so 1500 chars is a safe upper bound that still rejects oversized inputs early.
	maxContinuationTokenLen = 1500
	// maxContinuationKeyLen is the maximum accepted length of the decoded key
	// inside a continuation token, matching the S3 object-key limit.
	maxContinuationKeyLen = 1024
)

type listBucketResult struct {
	XMLName               xml.Name           `xml:"ListBucketResult"`
	Xmlns                 string             `xml:"xmlns,attr"`
	Name                  string             `xml:"Name"`
	Prefix                string             `xml:"Prefix"`
	KeyCount              int                `xml:"KeyCount"`
	MaxKeys               int                `xml:"MaxKeys"`
	Delimiter             string             `xml:"Delimiter,omitempty"`
	IsTruncated           bool               `xml:"IsTruncated"`
	Contents              []listBucketObject `xml:"Contents,omitempty"`
	CommonPrefixes        []listCommonPrefix `xml:"CommonPrefixes,omitempty"`
	ContinuationToken     string             `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string             `xml:"NextContinuationToken,omitempty"`
	StartAfter            string             `xml:"StartAfter,omitempty"`
	EncodingType          string             `xml:"EncodingType,omitempty"`
}

type listBucketObject struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type listCommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type listEntry struct {
	value  string
	cursor string
	object *metadata.Object
}

type listObjectsV2Params struct {
	prefix             string
	delimiter          string
	continuationToken  string
	startAfter         string
	responseStartAfter string
	hasStartAfter      bool
	encodingType       string
	maxKeys            int
}

func (h *ObjectHandlers) ListObjectsV2(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("list-type") != "2" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	bucketName := chiBucketParam(r)
	params, err := parseListObjectsV2Params(r)
	if errors.Is(err, errInvalidMaxKeys) {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidArgument, messageInvalidArgument)
		return
	}
	if err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionListBucket, "", params.prefix) {
		return
	}

	entries, isTruncated, err := h.listObjectsV2Entries(r, bucketName, params)
	if err != nil {
		h.logError("list bucket objects", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	result := buildListBucketResult(bucketName, params, entries, isTruncated)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(result)
}

func parseListObjectsV2Params(r *http.Request) (listObjectsV2Params, error) {
	query := r.URL.Query()
	maxKeys := defaultListMaxKeys
	if rawMaxKeys := strings.TrimSpace(query.Get("max-keys")); rawMaxKeys != "" {
		parsedMaxKeys, err := strconv.Atoi(rawMaxKeys)
		if err != nil || parsedMaxKeys < 0 {
			return listObjectsV2Params{}, errInvalidMaxKeys
		}
		maxKeys = parsedMaxKeys
	}
	if maxKeys > maxListKeys {
		maxKeys = maxListKeys
	}

	encodingType := query.Get("encoding-type")
	if encodingType != "" && encodingType != "url" {
		return listObjectsV2Params{}, errors.New("invalid encoding-type")
	}

	startAfter := query.Get("start-after")
	responseStartAfter := startAfter
	hasStartAfter := query.Has("start-after")
	continuationToken := query.Get("continuation-token")
	if continuationToken != "" {
		decodedToken, err := decodeContinuationToken(continuationToken)
		if err != nil {
			return listObjectsV2Params{}, err
		}
		startAfter = decodedToken
	}

	return listObjectsV2Params{
		prefix:             query.Get("prefix"),
		delimiter:          query.Get("delimiter"),
		continuationToken:  continuationToken,
		startAfter:         startAfter,
		responseStartAfter: responseStartAfter,
		hasStartAfter:      hasStartAfter,
		encodingType:       encodingType,
		maxKeys:            maxKeys,
	}, nil
}

func (h *ObjectHandlers) listObjectsV2Entries(r *http.Request, bucketName string, params listObjectsV2Params) ([]listEntry, bool, error) {
	if params.maxKeys == 0 {
		return nil, false, nil
	}

	if params.delimiter == "" {
		objects, isTruncated, err := h.Objects.List(r.Context(), bucketName, params.prefix, params.startAfter, params.maxKeys)
		if err != nil {
			return nil, false, err
		}

		entries := make([]listEntry, 0, len(objects))
		for i := range objects {
			entries = append(entries, listEntry{value: objects[i].Key, cursor: objects[i].Key, object: &objects[i]})
		}
		return entries, isTruncated, nil
	}

	results, err := h.Objects.ListDelimited(r.Context(), bucketName, params.prefix, params.startAfter, params.delimiter, params.maxKeys)
	if err != nil {
		return nil, false, err
	}

	isTruncated := len(results) > params.maxKeys
	if isTruncated {
		results = results[:params.maxKeys]
	}

	entries := make([]listEntry, 0, len(results))
	for _, res := range results {
		if res.IsPrefix {
			entries = append(entries, listEntry{value: res.VirtualKey, cursor: res.CursorKey})
		} else {
			entries = append(entries, listEntry{value: res.VirtualKey, cursor: res.CursorKey, object: res.Object})
		}
	}
	return entries, isTruncated, nil
}

func buildListBucketResult(bucketName string, params listObjectsV2Params, entries []listEntry, isTruncated bool) listBucketResult {
	result := listBucketResult{
		Xmlns:             "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:              bucketName,
		Prefix:            encodeListValue(params.prefix, params.encodingType),
		KeyCount:          0,
		MaxKeys:           params.maxKeys,
		Delimiter:         encodeListValue(params.delimiter, params.encodingType),
		IsTruncated:       false,
		ContinuationToken: params.continuationToken,
		EncodingType:      params.encodingType,
	}
	if params.hasStartAfter {
		result.StartAfter = encodeListValue(params.responseStartAfter, params.encodingType)
	}

	if isTruncated && len(entries) > 0 {
		result.IsTruncated = true
		result.NextContinuationToken = encodeContinuationToken(entries[len(entries)-1].cursor)
	}

	for _, entry := range entries {
		if entry.object == nil {
			result.CommonPrefixes = append(result.CommonPrefixes, listCommonPrefix{
				Prefix: encodeListValue(entry.value, params.encodingType),
			})
			continue
		}

		result.Contents = append(result.Contents, listBucketObject{
			Key:          encodeListValue(entry.object.Key, params.encodingType),
			LastModified: entry.object.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
			ETag:         quoteETag(entry.object.ETag),
			Size:         entry.object.Size,
			StorageClass: "STANDARD",
		})
	}

	result.KeyCount = len(result.Contents) + len(result.CommonPrefixes)
	return result
}

func encodeContinuationToken(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func decodeContinuationToken(token string) (string, error) {
	if len(token) > maxContinuationTokenLen {
		return "", fmt.Errorf("continuation token exceeds maximum length of %d characters", maxContinuationTokenLen)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}
	if len(decoded) > maxContinuationKeyLen {
		return "", fmt.Errorf("decoded continuation key exceeds maximum length of %d bytes", maxContinuationKeyLen)
	}
	return string(decoded), nil
}

func encodeListValue(value, encodingType string) string {
	if encodingType != "url" {
		return value
	}
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func chiBucketParam(r *http.Request) string {
	return strings.TrimSpace(chi.URLParam(r, "bucket"))
}
