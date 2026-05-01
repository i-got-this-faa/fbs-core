package s3

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

const (
	defaultListMaxKeys  = 1000
	maxListKeys         = 1000
	listObjectsPageSize = 1000
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
	prefix            string
	delimiter         string
	continuationToken string
	startAfter        string
	encodingType      string
	maxKeys           int
}

func (h *ObjectHandlers) ListObjectsV2(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("list-type") != "2" {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	bucketName := chiBucketParam(r)
	if !h.ensureBucket(w, r, bucketName) {
		return
	}

	params, err := parseListObjectsV2Params(r)
	if err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}

	entries, err := h.listObjectsV2Entries(r, bucketName, params)
	if err != nil {
		h.logError("list bucket objects", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	result := buildListBucketResult(bucketName, params, entries)
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
			return listObjectsV2Params{}, errors.New("invalid max-keys")
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
	continuationToken := query.Get("continuation-token")
	if continuationToken != "" {
		decodedToken, err := decodeContinuationToken(continuationToken)
		if err != nil {
			return listObjectsV2Params{}, err
		}
		startAfter = decodedToken
	}

	return listObjectsV2Params{
		prefix:            query.Get("prefix"),
		delimiter:         query.Get("delimiter"),
		continuationToken: continuationToken,
		startAfter:        startAfter,
		encodingType:      encodingType,
		maxKeys:           maxKeys,
	}, nil
}

func (h *ObjectHandlers) listObjectsV2Entries(r *http.Request, bucketName string, params listObjectsV2Params) ([]listEntry, error) {
	if params.maxKeys == 0 {
		return nil, nil
	}

	if params.delimiter == "" {
		objects, _, err := h.Objects.List(r.Context(), bucketName, params.prefix, params.startAfter, params.maxKeys+1)
		if err != nil {
			return nil, err
		}

		entries := make([]listEntry, 0, len(objects))
		for i := range objects {
			entries = append(entries, listEntry{value: objects[i].Key, cursor: objects[i].Key, object: &objects[i]})
		}
		return entries, nil
	}

	return h.listDelimitedObjectsV2Entries(r, bucketName, params)
}

func (h *ObjectHandlers) listDelimitedObjectsV2Entries(r *http.Request, bucketName string, params listObjectsV2Params) ([]listEntry, error) {
	startAfter := params.startAfter
	objectEntries := make([]listEntry, 0, params.maxKeys+1)
	commonPrefixCursors := make(map[string]string)

	for {
		objects, isTruncated, err := h.Objects.List(r.Context(), bucketName, params.prefix, startAfter, listObjectsPageSize)
		if err != nil {
			return nil, err
		}
		if len(objects) == 0 {
			return mergeDelimitedEntries(objectEntries, commonPrefixCursors), nil
		}

		for i := range objects {
			addDelimitedEntry(objects[i], params.prefix, params.delimiter, &objectEntries, commonPrefixCursors)
		}

		entries := mergeDelimitedEntries(objectEntries, commonPrefixCursors)
		if len(entries) > params.maxKeys || !isTruncated {
			return entries, nil
		}

		startAfter = objects[len(objects)-1].Key
	}
}

func addDelimitedEntry(object metadata.Object, prefix, delimiter string, objectEntries *[]listEntry, commonPrefixCursors map[string]string) {
	key := object.Key
	remainder := strings.TrimPrefix(key, prefix)
	delimiterIndex := strings.Index(remainder, delimiter)
	if delimiterIndex < 0 {
		*objectEntries = append(*objectEntries, listEntry{value: key, cursor: key, object: &object})
		return
	}

	commonPrefix := prefix + remainder[:delimiterIndex+len(delimiter)]
	if key > commonPrefixCursors[commonPrefix] {
		commonPrefixCursors[commonPrefix] = key
	}
}

func mergeDelimitedEntries(objectEntries []listEntry, commonPrefixCursors map[string]string) []listEntry {
	entries := make([]listEntry, 0, len(objectEntries)+len(commonPrefixCursors))
	entries = append(entries, objectEntries...)
	for commonPrefix, cursor := range commonPrefixCursors {
		entries = append(entries, listEntry{value: commonPrefix, cursor: cursor})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].value < entries[j].value
	})

	return entries
}

func buildListBucketResult(bucketName string, params listObjectsV2Params, entries []listEntry) listBucketResult {
	result := listBucketResult{
		Xmlns:             "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:              bucketName,
		Prefix:            encodeListValue(params.prefix, params.encodingType),
		KeyCount:          0,
		MaxKeys:           params.maxKeys,
		Delimiter:         encodeListValue(params.delimiter, params.encodingType),
		IsTruncated:       false,
		ContinuationToken: params.continuationToken,
		StartAfter:        encodeListValue(params.startAfter, params.encodingType),
		EncodingType:      params.encodingType,
	}

	visibleEntries := entries
	if len(entries) > params.maxKeys {
		result.IsTruncated = true
		visibleEntries = entries[:params.maxKeys]
		result.NextContinuationToken = encodeContinuationToken(visibleEntries[len(visibleEntries)-1].cursor)
	}

	for _, entry := range visibleEntries {
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
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func encodeListValue(value, encodingType string) string {
	if encodingType != "url" {
		return value
	}
	return url.QueryEscape(value)
}

func chiBucketParam(r *http.Request) string {
	return strings.TrimSpace(chi.URLParam(r, "bucket"))
}
