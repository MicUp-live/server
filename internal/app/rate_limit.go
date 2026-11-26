// Package app implements lightweight HTTP protections and distributed rate limiting.
package app

import (
	"context"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"micup.live/server/internal/store"
)

type rateLimitEntry struct {
	count   int
	resetAt time.Time
}

type rateLimiter struct {
	store   store.RateLimitStore
	scope   string
	limit   int
	window  time.Duration
	mu      sync.Mutex
	entries map[string]rateLimitEntry
}

func newRateLimiter(rateStore store.RateLimitStore, scope string, limit int, window time.Duration) *rateLimiter {
	if limit < 1 {
		limit = 1
	}
	if window < time.Second {
		window = time.Minute
	}
	return &rateLimiter{
		store:   rateStore,
		scope:   scope,
		limit:   limit,
		window:  window,
		entries: make(map[string]rateLimitEntry),
	}
}

func (l *rateLimiter) middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		allowed, remaining, retryAfter := l.allow(r.Context(), ip)

		w.Header().Set("RateLimit-Limit", strconv.Itoa(l.limit))
		w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))

		if !allowed {
			retrySeconds := int(retryAfter.Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limited","message":"too many requests; try again shortly"}`))
			return
		}

		next.ServeHTTP(w, r)
	}
}

func (l *rateLimiter) allow(ctx context.Context, key string) (bool, int, time.Duration) {
	if l.store != nil {
		res, err := l.store.Allow(ctx, l.scope, key, l.limit, l.window)
		if err == nil {
			retryAfter := time.Until(res.ResetAt)
			if retryAfter < 0 {
				retryAfter = 0
			}
			return res.Allowed, res.Remaining, retryAfter
		}
		log.Printf("[RATE LIMIT WARN] Distributed rate limit check failed for scope=%s key=%s: %v; falling back to in-memory", l.scope, key, err)
	}

	return l.allowInMemory(key)
}

func (l *rateLimiter) allowInMemory(key string) (bool, int, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	for entryKey, entry := range l.entries {
		if !now.Before(entry.resetAt) {
			delete(l.entries, entryKey)
		}
	}

	entry := l.entries[key]
	if entry.resetAt.IsZero() || !now.Before(entry.resetAt) {
		entry = rateLimitEntry{resetAt: now.Add(l.window)}
	}
	entry.count++
	l.entries[key] = entry

	remaining := l.limit - entry.count
	if remaining < 0 {
		remaining = 0
	}
	retryAfter := entry.resetAt.Sub(now)
	if retryAfter < 0 {
		retryAfter = 0
	}

	return entry.count <= l.limit, remaining, retryAfter
}

// clientIP extracts and validates the peer address set by the HTTP server.
//
// In production, aws-lambda-go-api-proxy sets RemoteAddr from API Gateway's
// requestContext.http.sourceIp. Forwarding headers are deliberately ignored:
// CloudFront appends the real viewer address to an existing X-Forwarded-For
// value, so trusting any caller-controlled position would let clients choose
// their own rate-limit key.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}
	if ip := net.ParseIP(r.RemoteAddr); ip != nil {
		return ip.String()
	}

	if r.RemoteAddr == "" {
		return "127.0.0.1"
	}
	return r.RemoteAddr
}
