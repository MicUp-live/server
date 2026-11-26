package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"micup.live/server/internal/config"
)

func newRouterForTest(t *testing.T) http.Handler {
	t.Helper()
	router, err := NewRouter(config.Config{
		Environment:    config.Development,
		Port:           "8080",
		AuthSecret:     "test-signing-secret-with-at-least-32-bytes",
		AllowedOrigins: []string{"https://micup.live"},
	}, Dependencies{})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}
	return router
}

func TestRouterHealthEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	newRouterForTest(t).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("health status = %d; want %d", response.Code, http.StatusNoContent)
	}
}

func TestRouterProtectsSongbookRoutes(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/songbooks", nil)
	response := httptest.NewRecorder()

	newRouterForTest(t).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("songbooks status = %d; want %d", response.Code, http.StatusUnauthorized)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q; want application/json", response.Header().Get("Content-Type"))
	}
}

func TestRouterAppliesCORSBeforeRoutes(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/events/ev-123/requests/reorder", nil)
	request.Header.Set("Origin", "https://micup.live")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "Idempotency-Key, Content-Type")
	response := httptest.NewRecorder()

	newRouterForTest(t).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d; want %d", response.Code, http.StatusNoContent)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://micup.live" {
		t.Fatalf("allow origin = %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("expected credentials to be allowed")
	}
	if !strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key") {
		t.Fatalf("expected Idempotency-Key in allow headers: %q", response.Header().Get("Access-Control-Allow-Headers"))
	}
	if !strings.Contains(response.Header().Get("Access-Control-Expose-Headers"), "X-Queue-Version") {
		t.Fatalf("expected X-Queue-Version in expose headers: %q", response.Header().Get("Access-Control-Expose-Headers"))
	}
}

func TestRouterRejectsDisallowedCORSOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/events/ev-123/requests/reorder", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()

	newRouterForTest(t).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("preflight status = %d; want %d", response.Code, http.StatusForbidden)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin received allow origin: %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
}
