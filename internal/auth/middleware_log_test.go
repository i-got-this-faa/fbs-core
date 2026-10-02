package auth

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogInternalErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		wantLog bool
	}{
		{name: "internal error is logged", err: internalError(errors.New("database connection lost")), wantLog: true},
		{name: "client error is not logged", err: ErrInvalidCredentials, wantLog: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logs := &bytes.Buffer{}
			var gotErr error
			respond := LogInternalErrors(slog.New(slog.NewTextHandler(logs, nil)), func(w http.ResponseWriter, _ *http.Request, err error) {
				gotErr = err
				w.WriteHeader(http.StatusTeapot)
			})

			rec := httptest.NewRecorder()
			respond(rec, httptest.NewRequest(http.MethodGet, "/api/management/users", nil), tt.err)

			if gotErr != tt.err || rec.Code != http.StatusTeapot {
				t.Fatalf("wrapped responder got err %v, status %d", gotErr, rec.Code)
			}
			logged := strings.Contains(logs.String(), "database connection lost")
			if logged != tt.wantLog {
				t.Fatalf("logged cause = %t, want %t; logs: %s", logged, tt.wantLog, logs.String())
			}
			if tt.wantLog && !strings.Contains(logs.String(), "path=/api/management/users") {
				t.Fatalf("log is missing the request path: %s", logs.String())
			}
		})
	}
}
