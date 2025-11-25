package middleware

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"time"

	"log/slog"
)

// RequestLogOptions tunes the behavior of the request logger middleware.
type RequestLogOptions struct {
	MaxBodyBytes int
}

const defaultMaxLogBody = 4096

// RequestLogger wraps an http.Handler and logs request/response metadata once the
// handler completes.
func RequestLogger(log *slog.Logger, opts RequestLogOptions) func(http.Handler) http.Handler {
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = defaultMaxLogBody
	}
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqBody, reqSize := captureRequestBody(r, maxBody)
			lrw := newLoggingResponseWriter(w, maxBody)
			next.ServeHTTP(lrw, r)
			duration := time.Since(start)
			log.Info("api request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("query", r.URL.RawQuery),
				slog.Int("status", lrw.statusCode()),
				slog.Duration("duration", duration),
				slog.Int("request_size", reqSize),
				slog.String("request_body", reqBody),
				slog.Int("response_size", lrw.size),
				slog.String("response_body", lrw.bodyString()),
			)
		})
	}
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status  int
	maxBody int
	body    bytes.Buffer
	size    int
}

func newLoggingResponseWriter(w http.ResponseWriter, maxBody int) *loggingResponseWriter {
	return &loggingResponseWriter{ResponseWriter: w, maxBody: maxBody}
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len() < w.maxBody {
		remaining := w.maxBody - w.body.Len()
		if remaining > len(b) {
			remaining = len(b)
		}
		w.body.Write(b[:remaining])
	}
	w.size += len(b)
	return w.ResponseWriter.Write(b)
}

func (w *loggingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrHijacked
}

func (w *loggingResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *loggingResponseWriter) bodyString() string {
	if w.body.Len() >= w.maxBody {
		return w.body.String() + "..."
	}
	return w.body.String()
}

func captureRequestBody(r *http.Request, maxBody int) (string, int) {
	if r.Body == nil {
		return "", 0
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return "<read error>", 0
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	snippet := string(bodyBytes)
	if len(snippet) > maxBody {
		snippet = snippet[:maxBody] + "..."
	}
	return snippet, len(bodyBytes)
}
