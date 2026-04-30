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

	"github.com/google/uuid"
	"github.com/i-got-this-faa/fbs/internal/metadata"
)

const tokenPrefix = "fbsa_"

type IssuedToken struct {
	AccessKeyID string
	RawToken    string
	SecretHash  string
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

func CreateBearerToken(ctx context.Context, repo metadata.UserRepository, displayName, role string) (IssuedToken, *metadata.User, error) {
	issued, err := IssueBearerToken()
	if err != nil {
		return IssuedToken{}, nil, err
	}

	now := time.Now().UTC()
	user := &metadata.User{
		ID:          uuid.NewString(),
		DisplayName: displayName,
		AccessKeyID: issued.AccessKeyID,
		SecretHash:  issued.SecretHash,
		Role:        role,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := repo.Create(ctx, user); err != nil {
		return IssuedToken{}, nil, fmt.Errorf("create bearer token user: %w", err)
	}

	return issued, user, nil
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
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return tokenPrefix + hex.EncodeToString(b), nil
}

func generateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
