package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
	"uuid"

	"github.com/i-got-this-faa/fbs/internal/metadata"
)

const (
	tokenPrefix = "fbsa_"
	sigV4Prefix = "fbsv4_"
)

type IssuedToken struct {
	AccessKeyID string
	RawToken    string
	SecretHash  string
}

type SigV4Credentials struct {
	AccessKeyID string
	SecretKey   string
}

func IssueBearerToken() (IssuedToken, error) {
	accessKeyID, err := generateAccessKeyID()
	if err != nil {
		return IssuedToken{}, fmt.Errorf("generate access key id: %w", err)
	}

	secret, err := generateSecret()
	if err != nil {
		return IssuedToken{}, fmt.Errorf("generate secret: %w", err)
	}

	secretHash := hashSecret(secret)
	rawToken := accessKeyID + "." + secret

	return IssuedToken{
		AccessKeyID: accessKeyID,
		RawToken:    rawToken,
		SecretHash:  secretHash,
	}, nil
}

func IssueSigV4Credentials() (SigV4Credentials, error) {
	accessKeyID, err := generateID(sigV4Prefix)
	if err != nil {
		return SigV4Credentials{}, fmt.Errorf("generate sigv4 access key id: %w", err)
	}

	secretKey, err := generateSecret()
	if err != nil {
		return SigV4Credentials{}, fmt.Errorf("generate sigv4 secret key: %w", err)
	}

	return SigV4Credentials{
		AccessKeyID: accessKeyID,
		SecretKey:   secretKey,
	}, nil
}

func CreateBearerToken(ctx context.Context, repo metadata.UserRepository, displayName, role string) (IssuedToken, SigV4Credentials, *metadata.User, error) {
	return createUserCredentials(ctx, repo.Create, displayName, role, "create bearer token user")
}

func CreateFirstAdmin(ctx context.Context, repo metadata.BootstrapRepository, displayName string) (IssuedToken, SigV4Credentials, *metadata.User, error) {
	if displayName == "" {
		displayName = "Initial Admin"
	}
	return createUserCredentials(ctx, repo.CreateFirstUser, displayName, "admin", "create first admin user")
}

func createUserCredentials(
	ctx context.Context,
	createUser func(context.Context, *metadata.User) error,
	displayName string,
	role string,
	errPrefix string,
) (IssuedToken, SigV4Credentials, *metadata.User, error) {
	issued, err := IssueBearerToken()
	if err != nil {
		return IssuedToken{}, SigV4Credentials{}, nil, err
	}

	sigv4Creds, err := IssueSigV4Credentials()
	if err != nil {
		return IssuedToken{}, SigV4Credentials{}, nil, err
	}

	now := time.Now().UTC()
	user := &metadata.User{
		ID:               uuid.New().String(),
		DisplayName:      displayName,
		AccessKeyID:      issued.AccessKeyID,
		SecretHash:       issued.SecretHash,
		SigV4AccessKeyID: sigv4Creds.AccessKeyID,
		SigV4SecretKey:   sigv4Creds.SecretKey,
		Role:             role,
		IsActive:         true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := createUser(ctx, user); err != nil {
		return IssuedToken{}, SigV4Credentials{}, nil, fmt.Errorf("%s: %w", errPrefix, err)
	}

	// Secrets are presented through IssuedToken and SigV4Credentials. The
	// returned user is safe for response DTOs.
	user.SecretHash = ""
	user.SigV4SecretKey = ""

	return issued, sigv4Creds, user, nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func verifySecret(secret, storedHex string) bool {
	sum := sha256.Sum256([]byte(secret))

	expected, err := hex.DecodeString(storedHex)
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare(sum[:], expected) == 1
}

func generateAccessKeyID() (string, error) {
	return generateID(tokenPrefix)
}

func generateID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

func generateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
