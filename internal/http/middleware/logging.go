package middleware

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			recorder := &responseRecorder{ResponseWriter: w}

			next.ServeHTTP(recorder, r)

			logger.Info(
				"http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.statusCode(),
				"bytes", recorder.bytes,
				"duration", time.Since(startedAt),
				"remote_addr", r.RemoteAddr,
			)
		})
	}
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	bytesWritten, err := r.ResponseWriter.Write(body)
	r.bytes += bytesWritten
	return bytesWritten, err
}

func (r *responseRecorder) ReadFrom(src io.Reader) (int64, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	readerFrom, ok := r.ResponseWriter.(io.ReaderFrom)
	if ok {
		bytesRead, err := readerFrom.ReadFrom(src)
		r.bytes += int(bytesRead)
		return bytesRead, err
	}

	bytesRead, err := io.Copy(r.ResponseWriter, src)
	r.bytes += int(bytesRead)
	return bytesRead, err
}

func (r *responseRecorder) Flush() {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}

	return hijacker.Hijack()
}

func (r *responseRecorder) Push(target string, opts *http.PushOptions) error {
	pusher, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}

	return pusher.Push(target, opts)
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *responseRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}

	return r.status
}
