package management

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalErrorWithoutLogger(t *testing.T) {
	t.Parallel()

	h := &Handlers{}
	rec := httptest.NewRecorder()
	h.internalError(rec, httptest.NewRequest(http.MethodGet, "/api/management/users", nil), "failed to list users", errors.New("database connection lost"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
