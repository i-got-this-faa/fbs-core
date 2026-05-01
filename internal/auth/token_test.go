package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i-got-this-faa/fbs/internal/metadata"
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

func TestIssueSigV4Credentials(t *testing.T) {
	t.Parallel()

	creds, err := IssueSigV4Credentials()
	if err != nil {
		t.Fatalf("IssueSigV4Credentials() error = %v", err)
	}

	if creds.AccessKeyID == "" {
		t.Error("AccessKeyID is empty")
	}
	if !strings.HasPrefix(creds.AccessKeyID, sigV4Prefix) {
		t.Errorf("AccessKeyID %q does not have expected prefix", creds.AccessKeyID)
	}
	if creds.SecretKey == "" {
		t.Error("SecretKey is empty")
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

func TestCreateBearerTokenPersistsActiveUser(t *testing.T) {
	t.Parallel()

	db, err := metadata.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	repo := metadata.NewUserRepository(db)
	sigv4Repo := metadata.NewSigV4UserRepository(db)
	issued, sigv4Creds, user, err := CreateBearerToken(context.Background(), repo, "Admin User", "admin")
	if err != nil {
		t.Fatalf("CreateBearerToken() error = %v", err)
	}

	stored, err := repo.GetByAccessKeyID(context.Background(), issued.AccessKeyID)
	if err != nil {
		t.Fatalf("get user by access key: %v", err)
	}

	if stored.ID != user.ID {
		t.Errorf("stored ID = %q, want %q", stored.ID, user.ID)
	}
	if stored.DisplayName != "Admin User" {
		t.Errorf("DisplayName = %q, want Admin User", stored.DisplayName)
	}
	if stored.Role != "admin" {
		t.Errorf("Role = %q, want admin", stored.Role)
	}
	if !stored.IsActive {
		t.Error("expected persisted user to be active")
	}
	if stored.SecretHash != issued.SecretHash {
		t.Error("persisted hash does not match issued hash")
	}
	if stored.SigV4AccessKeyID == "" {
		t.Error("expected persisted user to have sigv4_access_key_id")
	}
	// SigV4 secret is never retrievable through ordinary reads;
	// it is presented once at creation time via SigV4Credentials.
	if stored.SigV4SecretKey != "" {
		t.Error("expected SigV4SecretKey to be cleared on ordinary reads")
	}
	if strings.Contains(stored.SecretHash, issued.RawToken) {
		t.Error("stored hash should not contain raw token")
	}
	if sigv4Creds.AccessKeyID == "" {
		t.Error("expected returned SigV4 AccessKeyID to be non-empty")
	}
	if sigv4Creds.SecretKey == "" {
		t.Error("expected returned SigV4 SecretKey to be non-empty")
	}
	if sigv4Creds.AccessKeyID != stored.SigV4AccessKeyID {
		t.Error("returned SigV4 AccessKeyID does not match stored value")
	}
	// Secret is intentionally cleared on ordinary reads; verify via auth lookup below.

	// Verify the secret is retrievable only via the auth-specific lookup.
	authUser, err := sigv4Repo.GetBySigV4AccessKeyID(context.Background(), sigv4Creds.AccessKeyID)
	if err != nil {
		t.Fatalf("GetBySigV4AccessKeyID: %v", err)
	}
	if authUser.SigV4SecretKey != sigv4Creds.SecretKey {
		t.Errorf("auth lookup secret = %q, want %q", authUser.SigV4SecretKey, sigv4Creds.SecretKey)
	}
}
