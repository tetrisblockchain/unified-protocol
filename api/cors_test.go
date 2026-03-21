package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWrapWithCORSAllowsConfiguredOrigin(t *testing.T) {
	t.Parallel()

	handler := WrapWithCORS(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}), CORSConfig{AllowedOrigins: []string{"https://app.unified.network"}})

	request := httptest.NewRequest(http.MethodPost, "/rpc", nil)
	request.Header.Set("Origin", "https://app.unified.network")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://app.unified.network" {
		t.Fatalf("allow origin = %q, want configured origin", got)
	}
}

func TestWrapWithCORSHandlesPreflight(t *testing.T) {
	t.Parallel()

	handler := WrapWithCORS(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Fatalf("preflight should not reach downstream handler")
	}), CORSConfig{AllowedOrigins: []string{"*"}})

	request := httptest.NewRequest(http.MethodOptions, "/rpc", nil)
	request.Header.Set("Origin", "https://dashboard.example")
	request.Header.Set("Access-Control-Request-Headers", "content-type,x-api-key")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow origin = %q, want *", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); got != "content-type,x-api-key" {
		t.Fatalf("allow headers = %q, want request headers", got)
	}
}

func TestWrapWithCORSRejectsUnknownPreflightOrigin(t *testing.T) {
	t.Parallel()

	handler := WrapWithCORS(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Fatalf("rejected preflight should not reach downstream handler")
	}), CORSConfig{AllowedOrigins: []string{"https://app.unified.network"}})

	request := httptest.NewRequest(http.MethodOptions, "/rpc", nil)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}
