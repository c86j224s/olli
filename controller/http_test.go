package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPServerRejectsRemoteBindWithoutOptInOrToken(t *testing.T) {
	if _, _, err := validateHTTPOptions(HTTPOptions{Bind: "0.0.0.0:0"}); err == nil {
		t.Fatal("expected remote bind rejection")
	}
	if _, _, err := validateHTTPOptions(HTTPOptions{Bind: ":8766"}); err == nil {
		t.Fatal("expected wildcard bind rejection")
	}
	if _, _, err := validateHTTPOptions(HTTPOptions{Bind: "0.0.0.0:0", AllowRemoteBind: true}); err == nil {
		t.Fatal("expected missing token rejection")
	}
}

func TestHTTPMiddlewareRequiresBearerToken(t *testing.T) {
	h := &HTTPServer{service: &Service{}, token: strings.Repeat("a", 24), origins: map[string]struct{}{}}
	handler := h.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	request, _ := http.NewRequest(http.MethodGet, "http://controller/api/v1/info", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 24))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated status=%d", recorder.Code)
	}
}

func TestHTTPMiddlewareRejectsUnlistedOrigin(t *testing.T) {
	h := &HTTPServer{service: &Service{}, origins: map[string]struct{}{"https://allowed.example": {}}}
	handler := h.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	request, _ := http.NewRequest(http.MethodGet, "http://controller/api/v1/info", nil)
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d", recorder.Code)
	}
}
