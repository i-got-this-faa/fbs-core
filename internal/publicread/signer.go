package publicread

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrSigningDisabled    = errors.New("public read signing is disabled")
	ErrMalformedSignature = errors.New("malformed public read signature")
	ErrExpiredSignature   = errors.New("public read signature is expired")
	ErrInvalidSignature   = errors.New("invalid public read signature")
)

type Signer struct {
	secret []byte
	now    func() time.Time
}

func NewSigner(secret string, now func() time.Time) (*Signer, error) {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return nil, ErrSigningDisabled
	}
	if len([]byte(trimmed)) < 32 {
		return nil, fmt.Errorf("public read signing secret must be at least 32 bytes")
	}
	if now == nil {
		now = time.Now
	}

	return &Signer{secret: []byte(trimmed), now: now}, nil
}

func (s *Signer) SignPath(path string, expiresAt time.Time) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(canonicalString(path, expiresAt.Unix())))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Signer) Verify(path string, expiresUnix string, signatureHex string) error {
	expiresAtUnix, err := strconv.ParseInt(strings.TrimSpace(expiresUnix), 10, 64)
	if err != nil {
		return ErrMalformedSignature
	}
	if !s.now().Before(time.Unix(expiresAtUnix, 0)) {
		return ErrExpiredSignature
	}

	got, err := hex.DecodeString(strings.TrimSpace(signatureHex))
	if err != nil || len(got) != sha256.Size {
		return ErrMalformedSignature
	}

	wantHex := s.SignPath(path, time.Unix(expiresAtUnix, 0))
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		return ErrInvalidSignature
	}
	if !hmac.Equal(got, want) {
		return ErrInvalidSignature
	}

	return nil
}

func ObjectPath(bucketName, key string) string {
	escapedKeySegments := make([]string, 0, strings.Count(key, "/")+1)
	for _, segment := range strings.Split(key, "/") {
		escapedKeySegments = append(escapedKeySegments, url.PathEscape(segment))
	}

	return "/public/" + url.PathEscape(bucketName) + "/" + strings.Join(escapedKeySegments, "/")
}

func canonicalString(path string, expiresUnix int64) string {
	return "GET\n" + path + "\n" + strconv.FormatInt(expiresUnix, 10)
}
