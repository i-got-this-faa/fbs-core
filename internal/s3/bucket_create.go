package s3

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/auth"
	"github.com/i-got-this-faa/fbs/internal/metadata"
	"github.com/i-got-this-faa/fbs/internal/s3compat"
)

type createBucketConfiguration struct {
	XMLName            xml.Name `xml:"CreateBucketConfiguration"`
	LocationConstraint string   `xml:"LocationConstraint"`
}

func (h *ObjectHandlers) CreateBucket(w http.ResponseWriter, r *http.Request) {
	bucketName := chiBucketParam(r)
	if err := validateBucketName(bucketName); err != nil {
		WriteS3Error(w, r, http.StatusBadRequest, codeInvalidBucketName, messageInvalidBucketName)
		return
	}

	if err := validateCreateBucketConfiguration(r.Body); err != nil {
		switch {
		case errors.Is(err, errInvalidLocationConstraint):
			WriteS3Error(w, r, http.StatusBadRequest, codeInvalidLocationConstraint, messageInvalidLocationConstraint)
		default:
			WriteS3Error(w, r, http.StatusBadRequest, codeMalformedXML, messageMalformedXML)
		}
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || strings.TrimSpace(principal.UserID) == "" {
		WriteS3Error(w, r, http.StatusForbidden, codeAccessDenied, messageAccessDenied)
		return
	}
	ownerID, err := h.bucketOwnerID(r, principal)
	if err != nil {
		h.logError("resolve bucket owner", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	bucket, err := h.Buckets.GetByName(r.Context(), bucketName)
	if err == nil {
		h.writeCreateBucketConflict(w, r, bucket, ownerID)
		return
	}
	if !errors.Is(err, metadata.ErrBucketNotFound) {
		h.logError("load bucket before create", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}

	if err := h.Buckets.Create(r.Context(), &metadata.Bucket{
		Name:      bucketName,
		OwnerID:   ownerID,
		CreatedAt: h.now(),
	}); err != nil {
		if existingBucket, lookupErr := h.Buckets.GetByName(r.Context(), bucketName); lookupErr == nil {
			h.writeCreateBucketConflict(w, r, existingBucket, ownerID)
			return
		}
		h.logError("create bucket metadata", err, bucketName, "", "")
		WriteS3Error(w, r, http.StatusInternalServerError, codeInternalError, messageInternalError)
		return
	}
	h.recordActivity(r, "create_bucket", bucketName, "", 0, "")

	w.Header().Set("Location", "/"+bucketName)
	w.WriteHeader(http.StatusOK)
}

func (h *ObjectHandlers) writeCreateBucketConflict(w http.ResponseWriter, r *http.Request, bucket *metadata.Bucket, userID string) {
	code := codeBucketAlreadyExists
	message := messageBucketAlreadyExists
	if bucket != nil && bucket.OwnerID == userID {
		code = codeBucketAlreadyOwnedByYou
		message = messageBucketAlreadyOwnedByYou
	}
	WriteS3Error(w, r, http.StatusConflict, code, message)
}

func (h *ObjectHandlers) bucketOwnerID(r *http.Request, principal auth.Principal) (string, error) {
	if !principal.DevMode {
		return principal.UserID, nil
	}
	if h.Users == nil {
		return "", errors.New("user repository is required for dev bucket creation")
	}

	_, err := h.Users.GetByID(r.Context(), principal.UserID)
	if err == nil {
		return principal.UserID, nil
	}
	if !errors.Is(err, metadata.ErrUserNotFound) {
		return "", err
	}

	now := h.now()
	if err := h.Users.Create(r.Context(), &metadata.User{
		ID:          principal.UserID,
		DisplayName: principal.DisplayName,
		AccessKeyID: principal.AccessKeyID,
		SecretHash:  "dev-mode",
		Role:        principal.Role,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		if _, lookupErr := h.Users.GetByID(r.Context(), principal.UserID); lookupErr == nil {
			return principal.UserID, nil
		}
		return "", err
	}

	return principal.UserID, nil
}

var errInvalidLocationConstraint = errors.New("invalid location constraint")

func validateCreateBucketConfiguration(body io.ReadCloser) error {
	defer body.Close()

	payload, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}

	var config createBucketConfiguration
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&config); err != nil {
		return err
	}
	if config.XMLName.Local != "CreateBucketConfiguration" {
		return errors.New("unexpected root element")
	}

	locationConstraint := strings.TrimSpace(config.LocationConstraint)
	if locationConstraint == "" || locationConstraint == s3compat.Region {
		return nil
	}

	return errInvalidLocationConstraint
}

func validateBucketName(name string) error {
	if len(name) < 3 || len(name) > 63 {
		return errors.New("bucket name length out of range")
	}
	if name == "public" {
		return errors.New("bucket name is reserved")
	}
	if strings.ToLower(name) != name {
		return errors.New("bucket name must be lowercase")
	}
	if net.ParseIP(name) != nil {
		return errors.New("bucket name must not be an ip address")
	}

	for i := range len(name) {
		char := name[i]
		switch {
		case char >= 'a' && char <= 'z':
		case char >= '0' && char <= '9':
		case char == '-' || char == '.':
		default:
			return errors.New("bucket name contains invalid character")
		}
	}

	if !isBucketLabelChar(name[0]) || !isBucketLabelChar(name[len(name)-1]) {
		return errors.New("bucket name must start and end with a letter or number")
	}
	if strings.Contains(name, "..") || strings.Contains(name, ".-") || strings.Contains(name, "-.") {
		return errors.New("bucket name contains invalid adjacent punctuation")
	}

	return nil
}

func isBucketLabelChar(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
}
