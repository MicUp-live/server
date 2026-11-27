package app

import (
	"log"
	"net/http"
	"time"

	"micup.live/server/internal/config"
)

// NewHTTPServer configures the local and container HTTP server.
func NewHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

// loggingResponseWriter captures the HTTP status code and bytes written for logging.
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
	wroteHeader  bool
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	if !lrw.wroteHeader {
		lrw.statusCode = code
		lrw.wroteHeader = true
		lrw.ResponseWriter.WriteHeader(code)
	}
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	if !lrw.wroteHeader {
		lrw.WriteHeader(http.StatusOK)
	}
	n, err := lrw.ResponseWriter.Write(b)
	lrw.bytesWritten += int64(n)
	return n, err
}

// loggingMiddleware records incoming requests, response status, duration, and client details.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lrw, r)
		duration := time.Since(start).Round(time.Microsecond)

		// Query strings can carry one-time verification and cancellation tokens.
		// Log only the route, never caller-provided query values.
		path := r.URL.Path

		clientIP := clientIP(r)

		switch {
		case lrw.statusCode >= 500:
			log.Printf("[HTTP ERROR] %d %-6s %s (%s) from %s", lrw.statusCode, r.Method, path, duration, clientIP)
		case lrw.statusCode >= 400:
			log.Printf("[HTTP WARN]  %d %-6s %s (%s) from %s", lrw.statusCode, r.Method, path, duration, clientIP)
		default:
			if path != "/healthz" {
				log.Printf("[HTTP]       %d %-6s %s (%s) from %s", lrw.statusCode, r.Method, path, duration, clientIP)
			}
		}
	})
}

const (
	corsAllowedMethods = "GET, POST, PATCH, DELETE, OPTIONS"
	corsAllowedHeaders = "Content-Type, Authorization, X-Session-ID, X-Friendly-Name, Idempotency-Key, X-Cancel-Token, X-Edit-Token, X-API-Key, X-Deletion-Token"
	corsExposeHeaders  = "X-Queue-Version, Content-Disposition, RateLimit-Limit, RateLimit-Remaining, Retry-After, X-Deletion-Token"
)

func corsMiddleware(next http.Handler, origins []string) http.Handler {
	allowedOrigins := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowedOrigins[origin] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Add("Vary", "Origin")
		}

		isAllowed := false
		if _, wildcard := allowedOrigins["*"]; wildcard {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			isAllowed = true
		} else if _, allowed := allowedOrigins[origin]; allowed && origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			isAllowed = true
		}

		if isAllowed {
			w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			w.Header().Set("Access-Control-Expose-Headers", corsExposeHeaders)
		}

		if r.Method == http.MethodOptions {
			if isAllowed {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if origin != "" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
