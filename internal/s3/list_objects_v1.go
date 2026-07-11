package s3

import (
	"encoding/xml"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/authz"
)

type listBucketV1Result struct {
	XMLName        xml.Name           `xml:"ListBucketResult"`
	Xmlns          string             `xml:"xmlns,attr"`
	Name           string             `xml:"Name"`
	Prefix         string             `xml:"Prefix"`
	Marker         string             `xml:"Marker"`
	NextMarker     string             `xml:"NextMarker,omitempty"`
	MaxKeys        int                `xml:"MaxKeys"`
	Delimiter      string             `xml:"Delimiter,omitempty"`
	IsTruncated    bool               `xml:"IsTruncated"`
	Contents       []listBucketObject `xml:"Contents,omitempty"`
	CommonPrefixes []listCommonPrefix `xml:"CommonPrefixes,omitempty"`
	EncodingType   string             `xml:"EncodingType,omitempty"`
}

type listObjectsV1Params struct {
	prefix       string
	delimiter    string
	marker       string
	encodingType string
	maxKeys      int
}

func (h *ObjectHandlers) ListObjectsV1(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	params, err := parseListObjectsV1Params(r)
	if err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidRequest, messageInvalidRequest)
		return
	}
	if !h.ensureBucketAction(w, r, bucketName, authz.ActionListBucket, "", params.prefix) {
		return
	}

	entries, isTruncated, err := h.listObjectsV1Entries(r, bucketName, params)
	if err != nil {
		h.logError("list bucket objects v1", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	result := buildListBucketV1Result(bucketName, params, entries, isTruncated)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(result)
}

func parseListObjectsV1Params(r *http.Request) (listObjectsV1Params, error) {
	query := r.URL.Query()
	maxKeys := defaultListMaxKeys
	if rawMaxKeys := strings.TrimSpace(query.Get("max-keys")); rawMaxKeys != "" {
		parsedMaxKeys, err := strconv.Atoi(rawMaxKeys)
		if err != nil || parsedMaxKeys < 0 {
			return listObjectsV1Params{}, errors.New("invalid max-keys")
		}
		maxKeys = parsedMaxKeys
	}
	if maxKeys > maxListKeys {
		maxKeys = maxListKeys
	}

	encodingType := query.Get("encoding-type")
	if encodingType != "" && encodingType != "url" {
		return listObjectsV1Params{}, errors.New("invalid encoding-type")
	}

	return listObjectsV1Params{
		prefix:       query.Get("prefix"),
		delimiter:    query.Get("delimiter"),
		marker:       query.Get("marker"),
		encodingType: encodingType,
		maxKeys:      maxKeys,
	}, nil
}

func (h *ObjectHandlers) listObjectsV1Entries(r *http.Request, bucketName string, params listObjectsV1Params) ([]listEntry, bool, error) {
	v2Params := listObjectsV2Params{
		prefix:       params.prefix,
		delimiter:    params.delimiter,
		startAfter:   params.marker,
		encodingType: params.encodingType,
		maxKeys:      params.maxKeys,
	}
	return h.listObjectsV2Entries(r, bucketName, v2Params)
}

func buildListBucketV1Result(bucketName string, params listObjectsV1Params, entries []listEntry, isTruncated bool) listBucketV1Result {
	result := listBucketV1Result{
		Xmlns:        "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:         bucketName,
		Prefix:       encodeListValue(params.prefix, params.encodingType),
		Marker:       encodeListValue(params.marker, params.encodingType),
		MaxKeys:      params.maxKeys,
		Delimiter:    encodeListValue(params.delimiter, params.encodingType),
		IsTruncated:  isTruncated && len(entries) > 0,
		EncodingType: params.encodingType,
	}

	if result.IsTruncated {
		result.NextMarker = encodeListValue(entries[len(entries)-1].cursor, params.encodingType)
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

	return result
}
