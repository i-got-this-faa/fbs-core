package publicread

import (
	"errors"
	"testing"
	"time"
)

func TestSignerVerify(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	signer, err := NewSigner("12345678901234567890123456789012", func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewSigner error = %v", err)
	}

	path := ObjectPath("my bucket", "folder/a+b.txt")
	expiresAt := now.Add(time.Hour)
	signature := signer.SignPath(path, expiresAt, "")
	if err := signer.Verify(path, "1778670000", "", signature); err != nil {
		t.Fatalf("Verify error = %v", err)
	}

	if err := signer.Verify(path, "1778670000", "", "bd8bb140b5a66dc11101501125283703450a202ead8cb46777626167a64e7b0e"); err != nil {
		t.Fatalf("Verify legacy public URL signature error = %v", err)
	}

	disposition := `attachment; filename="archive.tar.gz"`
	downloadSignature := signer.SignPath(path, expiresAt, disposition)
	if err := signer.Verify(path, "1778670000", disposition, downloadSignature); err != nil {
		t.Fatalf("Verify download disposition error = %v", err)
	}
	if err := signer.Verify(path, "1778670000", `attachment; filename="other.tar.gz"`, downloadSignature); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify changed disposition error = %v, want ErrInvalidSignature", err)
	}
	if err := signer.Verify(path, "1778670000", "", "bad"); !errors.Is(err, ErrMalformedSignature) {
		t.Fatalf("Verify malformed error = %v, want ErrMalformedSignature", err)
	}
	if err := signer.Verify(path, "1778670000", "", signer.SignPath("/different", expiresAt, "")); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify invalid error = %v, want ErrInvalidSignature", err)
	}
	if err := signer.Verify(path, "1778666399", "", signer.SignPath(path, now.Add(-time.Second), "")); !errors.Is(err, ErrExpiredSignature) {
		t.Fatalf("Verify expired error = %v, want ErrExpiredSignature", err)
	}
}

func TestObjectPathEscapesSegmentsAndPreservesSlashes(t *testing.T) {
	t.Parallel()

	got := ObjectPath("my bucket", "folder/a+b/c d.txt")
	want := "/public/my%20bucket/folder/a+b/c%20d.txt"
	if got != want {
		t.Fatalf("ObjectPath = %q, want %q", got, want)
	}
}
