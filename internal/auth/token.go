package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
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
