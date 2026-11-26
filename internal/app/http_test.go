package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSMiddlewareAllowsConfiguredOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "https://micup.live")
	response := httptest.NewRecorder()

	corsMiddleware(next, []string{"https://micup.live"}).ServeHTTP(response, request)

	if response.Header().Get("Access-Control-Allow-Origin") != "https://micup.live" {
		t.Fatalf("allow origin = %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("allowed origin did not enable credentials")
	}
	if response.Header().Get("Vary") != "Origin" {
		t.Fatalf("vary = %q; want Origin", response.Header().Get("Vary"))
	}
	if !strings.Contains(response.Header().Get("Access-Control-Expose-Headers"), "X-Queue-Version") {
		t.Fatalf("expose headers = %q; want X-Queue-Version", response.Header().Get("Access-Control-Expose-Headers"))
	}
}

func TestCORSMiddlewareDoesNotReflectUnknownOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()

	corsMiddleware(http.NotFoundHandler(), []string{"https://micup.live"}).ServeHTTP(response, request)

	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unknown origin was allowed: %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
	if response.Header().Get("Access-Control-Expose-Headers") != "" {
		t.Fatalf("expose headers was set for unknown origin: %q", response.Header().Get("Access-Control-Expose-Headers"))
	}
}

func TestCORSMiddlewareHandlesPreflightWithoutCallingNext(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/songbooks", nil)
	request.Header.Set("Origin", "https://micup.live")
	response := httptest.NewRecorder()

	corsMiddleware(next, []string{"https://micup.live"}).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d; want %d", response.Code, http.StatusNoContent)
	}
	if called {
		t.Fatal("preflight called the downstream handler")
	}
}

func TestCORSMiddlewarePreflightAllowsIdempotencyKeyAndExposesQueueVersion(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/events/ev-123/requests/reorder", nil)
	request.Header.Set("Origin", "https://micup.live")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "Idempotency-Key, Content-Type, Authorization")
	response := httptest.NewRecorder()

	corsMiddleware(next, []string{"https://micup.live"}).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d; want %d", response.Code, http.StatusNoContent)
	}
	if called {
		t.Fatal("preflight called downstream handler")
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://micup.live" {
		t.Fatalf("allow origin = %q; want https://micup.live", response.Header().Get("Access-Control-Allow-Origin"))
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("preflight did not enable credentials")
	}

	allowedHeaders := response.Header().Get("Access-Control-Allow-Headers")
	for _, requiredHeader := range []string{"Idempotency-Key", "X-Cancel-Token", "X-API-Key", "X-Deletion-Token", "Authorization", "Content-Type"} {
		if !strings.Contains(allowedHeaders, requiredHeader) {
			t.Fatalf("allow headers %q missing %q", allowedHeaders, requiredHeader)
		}
	}

	exposeHeaders := response.Header().Get("Access-Control-Expose-Headers")
	for _, exposedHeader := range []string{"X-Queue-Version", "X-Deletion-Token", "Content-Disposition"} {
		if !strings.Contains(exposeHeaders, exposedHeader) {
			t.Fatalf("expose headers %q missing %s", exposeHeaders, exposedHeader)
		}
	}
}

func TestCORSMiddlewarePreflightRejectsDisallowedOrigin(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/events/ev-123/requests/reorder", nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "Idempotency-Key")
	response := httptest.NewRecorder()

	corsMiddleware(next, []string{"https://micup.live"}).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("preflight status = %d; want %d", response.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("preflight called downstream handler on disallowed origin")
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin received allow origin: %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
	if response.Header().Get("Access-Control-Allow-Headers") != "" {
		t.Fatalf("disallowed origin received allow headers: %q", response.Header().Get("Access-Control-Allow-Headers"))
	}
	if response.Header().Get("Access-Control-Expose-Headers") != "" {
		t.Fatalf("disallowed origin received expose headers: %q", response.Header().Get("Access-Control-Expose-Headers"))
	}
}

func TestCORSMiddlewareQueueResponses(t *testing.T) {
	cases := []struct {
		name           string
		method         string
		origin         string
		downstreamCode int
		queueVersion   string
		expectCode     int
		expectAllowed  bool
	}{
		{
			name:           "queue success 200",
			method:         http.MethodGet,
			origin:         "https://micup.live",
			downstreamCode: http.StatusOK,
			queueVersion:   "5",
			expectCode:     http.StatusOK,
			expectAllowed:  true,
		},
		{
			name:           "queue unchanged 204",
			method:         http.MethodGet,
			origin:         "https://micup.live",
			downstreamCode: http.StatusNoContent,
			queueVersion:   "5",
			expectCode:     http.StatusNoContent,
			expectAllowed:  true,
		},
		{
			name:           "queue conflict 409",
			method:         http.MethodPatch,
			origin:         "https://micup.live",
			downstreamCode: http.StatusConflict,
			queueVersion:   "5",
			expectCode:     http.StatusConflict,
			expectAllowed:  true,
		},
		{
			name:           "disallowed origin queue request",
			method:         http.MethodGet,
			origin:         "https://attacker.example",
			downstreamCode: http.StatusOK,
			queueVersion:   "5",
			expectCode:     http.StatusOK,
			expectAllowed:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Queue-Version", tc.queueVersion)
				w.WriteHeader(tc.downstreamCode)
			})

			request := httptest.NewRequest(tc.method, "/api/v1/events/ev-1/queue", nil)
			request.Header.Set("Origin", tc.origin)
			response := httptest.NewRecorder()

			corsMiddleware(next, []string{"https://micup.live"}).ServeHTTP(response, request)

			if response.Code != tc.expectCode {
				t.Fatalf("status = %d; want %d", response.Code, tc.expectCode)
			}
			if response.Header().Get("X-Queue-Version") != tc.queueVersion {
				t.Fatalf("X-Queue-Version = %q; want %q", response.Header().Get("X-Queue-Version"), tc.queueVersion)
			}

			if tc.expectAllowed {
				if response.Header().Get("Access-Control-Allow-Origin") != tc.origin {
					t.Fatalf("allow origin = %q; want %q", response.Header().Get("Access-Control-Allow-Origin"), tc.origin)
				}
				if response.Header().Get("Access-Control-Allow-Credentials") != "true" {
					t.Fatal("allow credentials not set")
				}
				exposeHeaders := response.Header().Get("Access-Control-Expose-Headers")
				if !strings.Contains(exposeHeaders, "X-Queue-Version") {
					t.Fatalf("expose headers %q does not contain X-Queue-Version", exposeHeaders)
				}
			} else {
				if response.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Fatalf("disallowed origin received allow origin: %q", response.Header().Get("Access-Control-Allow-Origin"))
				}
				if response.Header().Get("Access-Control-Expose-Headers") != "" {
					t.Fatalf("disallowed origin received expose headers: %q", response.Header().Get("Access-Control-Expose-Headers"))
				}
			}
		})
	}
}

func TestCORSMiddlewareWildcardOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Queue-Version", "3")
		w.WriteHeader(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-1/queue", nil)
	request.Header.Set("Origin", "https://third-party.example")
	response := httptest.NewRecorder()

	corsMiddleware(next, []string{"*"}).ServeHTTP(response, request)

	if response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("allow origin = %q; want *", response.Header().Get("Access-Control-Allow-Origin"))
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("wildcard origin must not allow credentials: %q", response.Header().Get("Access-Control-Allow-Credentials"))
	}
	if !strings.Contains(response.Header().Get("Access-Control-Expose-Headers"), "X-Queue-Version") {
		t.Fatalf("wildcard origin missing exposed X-Queue-Version header: %q", response.Header().Get("Access-Control-Expose-Headers"))
	}
}

func TestCORSMiddlewareNoOriginHeader(t *testing.T) {
	t.Run("GET without Origin", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		request := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
		response := httptest.NewRecorder()

		corsMiddleware(next, []string{"https://micup.live"}).ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200", response.Code)
		}
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("allow origin set on request without origin: %q", response.Header().Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("OPTIONS without Origin", func(t *testing.T) {
		next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("OPTIONS should not call next handler")
		})
		request := httptest.NewRequest(http.MethodOptions, "/api/v1/healthz", nil)
		response := httptest.NewRecorder()

		corsMiddleware(next, []string{"https://micup.live"}).ServeHTTP(response, request)

		if response.Code != http.StatusNoContent {
			t.Fatalf("status = %d; want 204", response.Code)
		}
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("allow origin set on OPTIONS without origin: %q", response.Header().Get("Access-Control-Allow-Origin"))
		}
	})
}
