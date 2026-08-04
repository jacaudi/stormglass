package tempestapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestClient_WithBaseURL_Proxy proves Proxy builds its request against the
// injected base URL (not the hardcoded WeatherFlow host), attaches the
// query, and injects the Bearer token server-side -- the seam Task 1.5's
// httpserver proxy handlers depend on.
func TestClient_WithBaseURL_Proxy(t *testing.T) {
	const token = "proxy-secret-token"
	var gotPath, gotQuery, gotAuth string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"forecast":{"daily":[]}}`))
	}))
	defer upstream.Close()

	client := NewClient(token, WithBaseURL(upstream.URL))

	query := url.Values{"station_id": {"12345"}}
	body, contentType, status, err := client.Proxy(t.Context(), "/better_forecast", query)
	if err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}

	if status != http.StatusOK {
		t.Errorf("Proxy() status = %d, want %d", status, http.StatusOK)
	}
	if contentType != "application/json; charset=utf-8" {
		t.Errorf("Proxy() contentType = %q, want upstream's Content-Type", contentType)
	}
	if !strings.Contains(string(body), "forecast") {
		t.Errorf("Proxy() body = %s, want upstream JSON passed through", body)
	}
	if gotPath != "/better_forecast" {
		t.Errorf("upstream request path = %q, want /better_forecast", gotPath)
	}
	if gotQuery != "station_id=12345" {
		t.Errorf("upstream request query = %q, want station_id=12345", gotQuery)
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("upstream Authorization = %q, want %q", gotAuth, "Bearer "+token)
	}
}

// TestClient_Proxy_DefaultsContentType proves a missing upstream
// Content-Type defaults to application/json rather than an empty string.
// This bypasses a real httptest.Server deliberately: Go's http.ResponseWriter
// always content-sniffs a Content-Type on Write when the handler doesn't set
// one, so a real round trip can never produce the empty-header case this
// test targets -- the mockRoundTripper (matching client_test.go's existing
// pattern) is the only way to hand Proxy a response with no Content-Type.
func TestClient_Proxy_DefaultsContentType(t *testing.T) {
	client := NewClient("tok")
	resp := mockResponse(http.StatusOK, `{}`)
	defer func() { _ = resp.Body.Close() }()
	client.http.Transport = &mockRoundTripper{response: resp}

	_, contentType, _, err := client.Proxy(t.Context(), "/stations", nil)
	if err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("Proxy() contentType = %q, want application/json default", contentType)
	}
}

// TestClient_Proxy_PassesThroughUpstreamStatus proves a non-200 upstream
// status is passed through as-is (passthrough proxy semantics), not turned
// into a client-side error.
func TestClient_Proxy_PassesThroughUpstreamStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer upstream.Close()

	client := NewClient("tok", WithBaseURL(upstream.URL))

	body, _, status, err := client.Proxy(t.Context(), "/stations", nil)
	if err != nil {
		t.Fatalf("Proxy() error = %v, want nil (upstream status passed through, not an error)", err)
	}
	if status != http.StatusTooManyRequests {
		t.Errorf("Proxy() status = %d, want %d", status, http.StatusTooManyRequests)
	}
	if !strings.Contains(string(body), "rate limited") {
		t.Errorf("Proxy() body = %s, want upstream error body passed through", body)
	}
}

// TestClient_Proxy_ScrubsTransportError proves a transport-level failure
// (here: connection refused) never leaks the token or the request URL in the
// returned error -- defensive scrubbing per design §15, even though the
// token itself travels only in the Authorization header, not the URL.
func TestClient_Proxy_ScrubsTransportError(t *testing.T) {
	const token = "must-not-leak-token"
	// Port 0 on loopback with no listener: guaranteed connection refused.
	client := NewClient(token, WithBaseURL("http://127.0.0.1:1"))

	_, _, _, err := client.Proxy(t.Context(), "/stations", nil)
	if err == nil {
		t.Fatal("Proxy() error = nil, want a transport error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("Proxy() error leaked the token: %v", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("Proxy() error leaked the request URL: %v", err)
	}
}
