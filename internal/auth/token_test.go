package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestIssueBearerToken(t *testing.T) {
	t.Parallel()

	issued, err := IssueBearerToken()
	if err != nil {
		t.Fatalf("IssueBearerToken() error = %v", err)
	}

	if issued.AccessKeyID == "" {
		t.Error("AccessKeyID is empty")
	}
	if !strings.HasPrefix(issued.AccessKeyID, tokenPrefix) {
		t.Errorf("AccessKeyID %q does not have expected prefix", issued.AccessKeyID)
	}

	parts := strings.Split(issued.RawToken, ".")
	if len(parts) != 2 {
		t.Fatalf("RawToken %q does not contain exactly one delimiter", issued.RawToken)
	}
	if parts[0] != issued.AccessKeyID {
		t.Errorf("raw token prefix %q != AccessKeyID %q", parts[0], issued.AccessKeyID)
	}

	if issued.SecretHash == "" {
		t.Error("SecretHash is empty")
	}

	expectedHash := sha256.Sum256([]byte(parts[1]))
	expectedHex := hex.EncodeToString(expectedHash[:])
	if issued.SecretHash != expectedHex {
		t.Error("SecretHash does not match SHA-256 of secret portion")
	}
}

func TestVerifySecret(t *testing.T) {
	t.Parallel()

	secret := "my-secret-value"
	stored := hashSecret(secret)

	if !verifySecret(secret, stored) {
		t.Error("verifySecret should return true for matching secret")
	}
	if verifySecret("wrong-secret", stored) {
		t.Error("verifySecret should return false for wrong secret")
	}
	if verifySecret(secret, "not-hex") {
		t.Error("verifySecret should return false for invalid hex")
	}
}

func TestIssueBearerToken_Unique(t *testing.T) {
	t.Parallel()

	issued1, err := IssueBearerToken()
	if err != nil {
		t.Fatalf("IssueBearerToken() error = %v", err)
	}
	issued2, err := IssueBearerToken()
	if err != nil {
		t.Fatalf("IssueBearerToken() error = %v", err)
	}

	if issued1.AccessKeyID == issued2.AccessKeyID {
		t.Error("expected unique AccessKeyIDs")
	}
	if issued1.RawToken == issued2.RawToken {
		t.Error("expected unique RawTokens")
	}
}
