package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/i-got-this-faa/fbs/internal/metadata"
)

const (
	sigV4Algorithm  = "AWS4-HMAC-SHA256"
	sigV4Terminator = "aws4_request"
	// EmptyStringHash is the hex-encoded SHA-256 hash of an empty string.
	EmptyStringHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	unsignedPayload = "UNSIGNED-PAYLOAD"
	maxClockSkew    = 15 * time.Minute
)

// SigV4Authenticator validates AWS Signature Version 4 requests.
type SigV4Authenticator struct {
	Repo metadata.SigV4UserRepository
	Now  func() time.Time
}

func (s *SigV4Authenticator) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *SigV4Authenticator) Authenticate(r *http.Request) (Principal, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		if strings.HasPrefix(authHeader, sigV4Algorithm) {
			return s.authenticateHeader(r)
		}
		return Principal{}, ErrNotApplicable
	}

	if r.URL.Query().Get("X-Amz-Algorithm") != "" {
		return s.authenticateQuery(r)
	}

	return Principal{}, ErrNotApplicable
}

func (s *SigV4Authenticator) authenticateHeader(r *http.Request) (Principal, error) {
	credential, signedHeaders, signature, err := parseAuthorizationHeader(r.Header.Get("Authorization"))
	if err != nil {
		return Principal{}, ErrMalformedToken
	}

	if err := validateSignedHeaders(signedHeaders); err != nil {
		return Principal{}, ErrMalformedToken
	}

	accessKeyID, credDate, region, service, err := parseCredential(credential)
	if err != nil {
		return Principal{}, ErrMalformedToken
	}

	rawDate := r.Header.Get("X-Amz-Date")
	if rawDate == "" {
		rawDate = r.Header.Get("Date")
	}

	timestamp, err := parseTimestamp(rawDate)
	if err != nil {
		return Principal{}, ErrMalformedToken
	}
	amzDate := timestamp.Format("20060102T150405Z")

	if err := validateTimestamp(amzDate, credDate, s.now()); err != nil {
		return Principal{}, ErrInvalidCredentials
	}

	user, err := s.Repo.GetBySigV4AccessKeyID(r.Context(), accessKeyID)
	if err != nil {
		if errors.Is(err, metadata.ErrUserNotFound) {
			return Principal{}, ErrInvalidCredentials
		}
		return Principal{}, ErrInternal
	}

	if !user.IsActive {
		return Principal{}, ErrInactiveUser
	}

	payloadHash := getPayloadHash(r)
	canonicalRequest := buildCanonicalRequest(r, signedHeaders, payloadHash)
	stringToSign := buildStringToSign(amzDate, credential, canonicalRequest)
	expectedSignature := computeSignature(user.SigV4SecretKey, credDate, region, service, stringToSign)

	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		return Principal{}, ErrInvalidCredentials
	}

	return Principal{
		UserID:      user.ID,
		DisplayName: user.DisplayName,
		AccessKeyID: user.SigV4AccessKeyID,
		Role:        user.Role,
		DevMode:     false,
	}, nil
}

func (s *SigV4Authenticator) authenticateQuery(r *http.Request) (Principal, error) {
	q := r.URL.Query()

	algorithm := q.Get("X-Amz-Algorithm")
	if algorithm != sigV4Algorithm {
		return Principal{}, ErrMalformedToken
	}

	credential := q.Get("X-Amz-Credential")
	signedHeaders := q.Get("X-Amz-SignedHeaders")
	signature := q.Get("X-Amz-Signature")
	amzDate := q.Get("X-Amz-Date")
	expires := q.Get("X-Amz-Expires")

	if credential == "" || signedHeaders == "" || signature == "" || amzDate == "" || expires == "" {
		return Principal{}, ErrMalformedToken
	}

	if err := validateSignedHeaders(signedHeaders); err != nil {
		return Principal{}, ErrMalformedToken
	}

	accessKeyID, credDate, region, service, err := parseCredential(credential)
	if err != nil {
		return Principal{}, ErrMalformedToken
	}

	timestamp, err := parseTimestamp(amzDate)
	if err != nil {
		return Principal{}, ErrMalformedToken
	}

	if timestamp.Format("20060102") != credDate {
		return Principal{}, ErrInvalidCredentials
	}

	// Reject signatures older than 7 days as a safety bound, regardless of X-Amz-Expires.
	if s.now().After(timestamp.Add(7 * 24 * time.Hour)) {
		return Principal{}, ErrInvalidCredentials
	}

	// Reject URLs signed too far in the future (symmetric with header auth clock-skew check).
	if timestamp.After(s.now().Add(maxClockSkew)) {
		return Principal{}, ErrInvalidCredentials
	}

	expirySeconds, err := strconv.Atoi(expires)
	if err != nil || expirySeconds <= 0 || expirySeconds > 604800 {
		return Principal{}, ErrMalformedToken
	}
	if s.now().After(timestamp.Add(time.Duration(expirySeconds) * time.Second)) {
		return Principal{}, ErrInvalidCredentials
	}

	user, err := s.Repo.GetBySigV4AccessKeyID(r.Context(), accessKeyID)
	if err != nil {
		if errors.Is(err, metadata.ErrUserNotFound) {
			return Principal{}, ErrInvalidCredentials
		}
		return Principal{}, ErrInternal
	}

	if !user.IsActive {
		return Principal{}, ErrInactiveUser
	}

	payloadHash := q.Get("X-Amz-Content-SHA256")
	if payloadHash == "" {
		payloadHash = unsignedPayload
	}

	canonicalRequest := buildCanonicalRequestForQuery(r, signedHeaders, payloadHash)
	stringToSign := buildStringToSign(amzDate, credential, canonicalRequest)
	expectedSignature := computeSignature(user.SigV4SecretKey, credDate, region, service, stringToSign)

	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		return Principal{}, ErrInvalidCredentials
	}

	return Principal{
		UserID:      user.ID,
		DisplayName: user.DisplayName,
		AccessKeyID: user.SigV4AccessKeyID,
		Role:        user.Role,
		DevMode:     false,
	}, nil
}

func parseAuthorizationHeader(header string) (credential, signedHeaders, signature string, err error) {
	// Format: AWS4-HMAC-SHA256 Credential=..., SignedHeaders=..., Signature=...
	if !strings.HasPrefix(header, sigV4Algorithm+" ") {
		return "", "", "", fmt.Errorf("invalid algorithm")
	}

	params := strings.TrimPrefix(header, sigV4Algorithm+" ")
	parts := strings.Split(params, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return "", "", "", fmt.Errorf("invalid param: %s", part)
		}
		key, value := kv[0], kv[1]
		switch key {
		case "Credential":
			credential = value
		case "SignedHeaders":
			signedHeaders = value
		case "Signature":
			signature = value
		}
	}

	if credential == "" || signedHeaders == "" || signature == "" {
		return "", "", "", fmt.Errorf("missing required param")
	}

	return credential, signedHeaders, signature, nil
}

func validateSignedHeaders(s string) error {
	for _, h := range strings.Split(s, ";") {
		if h == "host" {
			return nil
		}
	}
	return fmt.Errorf("missing required signed header: host")
}

func parseCredential(credential string) (accessKeyID, date, region, service string, err error) {
	// Format: access_key_id/YYYYMMDD/region/service/aws4_request
	parts := strings.Split(credential, "/")
	if len(parts) != 5 {
		return "", "", "", "", fmt.Errorf("invalid credential format")
	}
	if parts[4] != sigV4Terminator {
		return "", "", "", "", fmt.Errorf("invalid credential terminator")
	}
	accessKeyID, date, region, service = parts[0], parts[1], parts[2], parts[3]
	if accessKeyID == "" {
		return "", "", "", "", fmt.Errorf("empty access key id")
	}
	if len(date) != 8 {
		return "", "", "", "", fmt.Errorf("invalid date length")
	}
	for i := 0; i < 8; i++ {
		if date[i] < '0' || date[i] > '9' {
			return "", "", "", "", fmt.Errorf("invalid date format")
		}
	}
	if region == "" {
		return "", "", "", "", fmt.Errorf("empty region")
	}
	if service != "s3" {
		return "", "", "", "", fmt.Errorf("unsupported service")
	}
	return accessKeyID, date, region, service, nil
}

func parseTimestamp(s string) (time.Time, error) {
	if len(s) == 16 {
		return time.Parse("20060102T150405Z", s)
	}
	if len(s) == 20 {
		return time.Parse("20060102T150405.000Z", s)
	}
	// Fallback: HTTP Date header format (RFC 7231)
	if t, err := time.Parse(time.RFC1123, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp format")
}

func validateTimestamp(amzDate, credDate string, now time.Time) error {
	if amzDate == "" {
		return fmt.Errorf("missing timestamp")
	}

	t, err := parseTimestamp(amzDate)
	if err != nil {
		return fmt.Errorf("parse timestamp: %w", err)
	}

	if t.Format("20060102") != credDate {
		return fmt.Errorf("credential date mismatch")
	}

	diff := now.Sub(t)
	if diff < 0 {
		diff = -diff
	}
	if diff > maxClockSkew {
		return fmt.Errorf("request expired")
	}

	return nil
}

func getPayloadHash(r *http.Request) string {
	hash := r.Header.Get("X-Amz-Content-SHA256")
	if hash != "" {
		return hash
	}
	return unsignedPayload
}

func buildCanonicalRequest(r *http.Request, signedHeadersStr, payloadHash string) string {
	signedHeaders := strings.Split(signedHeadersStr, ";")

	canonicalURI := r.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	canonicalQueryString := buildCanonicalQueryString(r.URL, nil)
	canonicalHeaders := buildCanonicalHeaders(r, signedHeaders)

	return strings.Join([]string{
		r.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeadersStr,
		payloadHash,
	}, "\n")
}

func buildCanonicalRequestForQuery(r *http.Request, signedHeadersStr, payloadHash string) string {
	signedHeaders := strings.Split(signedHeadersStr, ";")

	canonicalURI := r.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	// Exclude X-Amz-Signature from canonical query string
	canonicalQueryString := buildCanonicalQueryString(r.URL, map[string]bool{
		"X-Amz-Signature": true,
	})
	canonicalHeaders := buildCanonicalHeaders(r, signedHeaders)

	return strings.Join([]string{
		r.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeadersStr,
		payloadHash,
	}, "\n")
}

func buildCanonicalHeaders(r *http.Request, signedHeaders []string) string {
	var parts []string
	for _, h := range signedHeaders {
		var value string
		switch h {
		case "host":
			value = r.Host
		default:
			value = r.Header.Get(h)
		}
		value = strings.TrimSpace(value)
		value = collapseWhitespace(value)
		parts = append(parts, h+":"+value+"\n")
	}
	return strings.Join(parts, "")
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	inSpace := false
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		} else {
			b.WriteRune(c)
			inSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

func buildCanonicalQueryString(u *url.URL, exclude map[string]bool) string {
	values := u.Query()

	var keys []string
	for k := range values {
		if exclude != nil && exclude[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		vals := values[k]
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k)+"="+uriEncode(v))
		}
	}

	return strings.Join(parts, "&")
}

func uriEncode(s string) string {
	spaceCount := 0
	for i := 0; i < len(s); i++ {
		if shouldEscape(s[i]) {
			spaceCount++
		}
	}

	if spaceCount == 0 {
		return s
	}

	var buf [64]byte
	var t []byte

	required := len(s) + 2*spaceCount
	if required <= len(buf) {
		t = buf[:required]
	} else {
		t = make([]byte, required)
	}

	j := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if shouldEscape(c) {
			t[j] = '%'
			t[j+1] = "0123456789ABCDEF"[c>>4]
			t[j+2] = "0123456789ABCDEF"[c&15]
			j += 3
		} else {
			t[j] = c
			j++
		}
	}

	return string(t)
}

func shouldEscape(c byte) bool {
	if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' {
		return false
	}
	switch c {
	case '-', '_', '.', '~':
		return false
	}
	return true
}

func buildStringToSign(timestamp, credentialScope, canonicalRequest string) string {
	date := timestamp[:8]
	scope := date + "/" + strings.Join(strings.Split(credentialScope, "/")[1:], "/")
	hash := sha256.Sum256([]byte(canonicalRequest))
	return strings.Join([]string{
		sigV4Algorithm,
		timestamp,
		scope,
		hex.EncodeToString(hash[:]),
	}, "\n")
}

func computeSignature(secretKey, date, region, service, stringToSign string) string {
	kSecret := []byte("AWS4" + secretKey)
	kDate := hmacSHA256(kSecret, date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, sigV4Terminator)
	signature := hmacSHA256(kSigning, stringToSign)
	return hex.EncodeToString(signature)
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// SignRequest is a test helper that signs an HTTP request with SigV4.
// It is exported so integration tests can use it.
func SignRequest(r *http.Request, accessKeyID, secretKey, region, service string, signedHeaders []string, payloadHash string) {
	if payloadHash == "" {
		payloadHash = unsignedPayload
	}

	now := time.Now().UTC()
	timestamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")

	r.Header.Set("X-Amz-Date", timestamp)

	if r.Header.Get("Host") == "" && r.Host != "" {
		r.Header.Set("Host", r.Host)
	}

	if payloadHash != unsignedPayload {
		r.Header.Set("X-Amz-Content-SHA256", payloadHash)
	}

	signedHeadersStr := strings.Join(signedHeaders, ";")
	canonicalRequest := buildCanonicalRequest(r, signedHeadersStr, payloadHash)

	credential := accessKeyID + "/" + date + "/" + region + "/" + service + "/" + sigV4Terminator
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(secretKey, date, region, service, stringToSign)

	authHeader := sigV4Algorithm +
		" Credential=" + credential +
		", SignedHeaders=" + signedHeadersStr +
		", Signature=" + signature

	r.Header.Set("Authorization", authHeader)
}

// SignRequestWithContext signs a request using the provided clock time.
func SignRequestWithContext(ctx context.Context, r *http.Request, accessKeyID, secretKey, region, service string, signedHeaders []string, payloadHash string, now time.Time) {
	_ = ctx // reserved for future extensibility
	if payloadHash == "" {
		payloadHash = unsignedPayload
	}

	timestamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")

	r.Header.Set("X-Amz-Date", timestamp)

	if r.Header.Get("Host") == "" && r.Host != "" {
		r.Header.Set("Host", r.Host)
	}

	if payloadHash != unsignedPayload {
		r.Header.Set("X-Amz-Content-SHA256", payloadHash)
	}

	signedHeadersStr := strings.Join(signedHeaders, ";")
	canonicalRequest := buildCanonicalRequest(r, signedHeadersStr, payloadHash)

	credential := accessKeyID + "/" + date + "/" + region + "/" + service + "/" + sigV4Terminator
	stringToSign := buildStringToSign(timestamp, credential, canonicalRequest)
	signature := computeSignature(secretKey, date, region, service, stringToSign)

	authHeader := sigV4Algorithm +
		" Credential=" + credential +
		", SignedHeaders=" + signedHeadersStr +
		", Signature=" + signature

	r.Header.Set("Authorization", authHeader)
}
