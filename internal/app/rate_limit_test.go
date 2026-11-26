package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	proxycore "github.com/awslabs/aws-lambda-go-api-proxy/core"

	"micup.live/server/internal/store"
)

func TestClientIPResolution(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		expectedIP string
	}{
		{
			name:       "Standard RemoteAddr with port",
			remoteAddr: "203.0.113.195:45821",
			expectedIP: "203.0.113.195",
		},
		{
			name:       "X-Forwarded-For cannot override peer",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "198.51.100.1"},
			expectedIP: "10.0.0.1",
		},
		{
			name:       "X-Forwarded-For chain cannot override peer",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "198.51.100.25, 70.32.1.2, 10.0.0.1"},
			expectedIP: "10.0.0.1",
		},
		{
			name:       "X-Real-IP cannot override peer",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Real-IP": "192.0.2.77"},
			expectedIP: "10.0.0.1",
		},
		{
			name:       "IPv6 client address",
			remoteAddr: "[2001:db8::1]:12345",
			expectedIP: "2001:db8::1",
		},
		{
			name:       "Invalid forwarded header is ignored",
			remoteAddr: "203.0.113.50:5000",
			headers:    map[string]string{"X-Forwarded-For": "invalid-ip-injection; DROP TABLE rate_limits;"},
			expectedIP: "203.0.113.50",
		},
		{
			name:       "Empty RemoteAddr fallback",
			remoteAddr: "",
			expectedIP: "127.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			ip := clientIP(req)
			if ip != tt.expectedIP {
				t.Errorf("expected IP %q, got %q", tt.expectedIP, ip)
			}
		})
	}
}

func TestClientIPUsesAPIGatewaySourceIP(t *testing.T) {
	event := events.APIGatewayV2HTTPRequest{
		Version: "2.0",
		RawPath: "/api/v1/auth/login",
		Headers: map[string]string{
			"x-forwarded-for": "192.0.2.10, 192.0.2.11",
			"x-real-ip":       "192.0.2.12",
		},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			DomainName: "example.execute-api.us-east-2.amazonaws.com",
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method:   http.MethodPost,
				Path:     "/api/v1/auth/login",
				SourceIP: "198.51.100.42",
			},
		},
	}

	request, err := (&proxycore.RequestAccessorV2{}).EventToRequestWithContext(context.Background(), event)
	if err != nil {
		t.Fatalf("convert API Gateway request: %v", err)
	}
	if got := clientIP(request); got != "198.51.100.42" {
		t.Fatalf("expected API Gateway source IP, got %q", got)
	}
}

func TestRateLimiter_InMemoryBehavior(t *testing.T) {
	limiter := newRateLimiter(nil, "auth", 2, time.Minute)
	middleware := limiter.middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	reqIP := "192.0.2.10:1234"

	// Request 1: Allowed
	req1 := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req1.RemoteAddr = reqIP
	w1 := httptest.NewRecorder()
	middleware(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}
	if w1.Header().Get("RateLimit-Limit") != "2" || w1.Header().Get("RateLimit-Remaining") != "1" {
		t.Errorf("unexpected headers on req1: limit=%s, remaining=%s",
			w1.Header().Get("RateLimit-Limit"), w1.Header().Get("RateLimit-Remaining"))
	}

	// Request 2: Allowed (hits limit)
	req2 := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req2.RemoteAddr = reqIP
	w2 := httptest.NewRecorder()
	middleware(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}
	if w2.Header().Get("RateLimit-Remaining") != "0" {
		t.Errorf("expected remaining=0, got %s", w2.Header().Get("RateLimit-Remaining"))
	}

	// Request 3: Blocked (exceeded limit)
	req3 := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req3.RemoteAddr = reqIP
	w3 := httptest.NewRecorder()
	middleware(w3, req3)
	if w3.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", w3.Code)
	}
	if w3.Header().Get("Retry-After") == "" {
		t.Errorf("expected Retry-After header to be set")
	}
	var errBody map[string]string
	if err := json.Unmarshal(w3.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errBody["error"] != "rate_limited" {
		t.Errorf("expected error=rate_limited, got %s", errBody["error"])
	}

	// Request from different IP: Allowed
	reqOther := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	reqOther.RemoteAddr = "192.0.2.99:1234"
	wOther := httptest.NewRecorder()
	middleware(wOther, reqOther)
	if wOther.Code != http.StatusOK {
		t.Fatalf("expected 200 for other IP, got %d", wOther.Code)
	}
}

type mockRateLimitStore struct {
	allowFunc func(ctx context.Context, scope, key string, limit int, window time.Duration) (store.RateLimitResult, error)
	called    bool
}

func (m *mockRateLimitStore) Allow(ctx context.Context, scope, key string, limit int, window time.Duration) (store.RateLimitResult, error) {
	m.called = true
	if m.allowFunc != nil {
		return m.allowFunc(ctx, scope, key, limit, window)
	}
	return store.RateLimitResult{Allowed: true, Limit: limit, Remaining: limit - 1, ResetAt: time.Now().Add(window)}, nil
}

func TestRateLimiter_DistributedStoreIntegration(t *testing.T) {
	mock := &mockRateLimitStore{
		allowFunc: func(ctx context.Context, scope, key string, limit int, window time.Duration) (store.RateLimitResult, error) {
			if key == "198.51.100.1" {
				return store.RateLimitResult{
					Allowed:   false,
					Limit:     limit,
					Remaining: 0,
					ResetAt:   time.Now().Add(45 * time.Second),
				}, nil
			}
			return store.RateLimitResult{
				Allowed:   true,
				Limit:     limit,
				Remaining: 5,
				ResetAt:   time.Now().Add(window),
			}, nil
		},
	}

	limiter := newRateLimiter(mock, "public_req", 10, time.Minute)
	middleware := limiter.middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// 1. Blocked IP
	reqBlocked := httptest.NewRequest(http.MethodPost, "/events/e1/requests", nil)
	reqBlocked.RemoteAddr = "198.51.100.1:443"
	wBlocked := httptest.NewRecorder()
	middleware(wBlocked, reqBlocked)
	if wBlocked.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", wBlocked.Code)
	}

	// 2. Allowed IP
	reqAllowed := httptest.NewRequest(http.MethodPost, "/events/e1/requests", nil)
	reqAllowed.RemoteAddr = "203.0.113.88:443"
	wAllowed := httptest.NewRecorder()
	middleware(wAllowed, reqAllowed)
	if wAllowed.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wAllowed.Code)
	}
}

func TestRateLimiter_FallbackOnStoreError(t *testing.T) {
	// If the distributed store errors out, the limiter must gracefully fall back
	// to in-memory without breaking user requests.
	mock := &mockRateLimitStore{
		allowFunc: func(ctx context.Context, scope, key string, limit int, window time.Duration) (store.RateLimitResult, error) {
			return store.RateLimitResult{}, errors.New("dynamodb network timeout")
		},
	}

	limiter := newRateLimiter(mock, "auth", 2, time.Minute)
	middleware := limiter.middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "192.0.2.55:4000"

	// Request 1: Store fails -> falls back to in-memory -> Allowed
	w1 := httptest.NewRecorder()
	middleware(w1, req)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 on fallback, got %d", w1.Code)
	}

	// Request 2: Store fails -> in-memory hits limit -> Allowed
	w2 := httptest.NewRecorder()
	middleware(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 on fallback req2, got %d", w2.Code)
	}

	// Request 3: Store fails -> in-memory exceeds limit -> 429
	w3 := httptest.NewRecorder()
	middleware(w3, req)
	if w3.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on in-memory fallback limit, got %d", w3.Code)
	}
}
