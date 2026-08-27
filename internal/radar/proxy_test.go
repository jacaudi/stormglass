package radar

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sidecarFeatureCollection writes a minimal-but-valid Contract A success body
// (a GeoJSON FeatureCollection with the metadata block the proxy decodes).
func sidecarFeatureCollection(w http.ResponseWriter, site, product string) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":     "FeatureCollection",
		"features": []any{},
		"metadata": map[string]any{
			"site":      site,
			"product":   product,
			"scan_time": "2026-07-20T12:00:00Z",
			"bbox":      []float64{-98.5, 34.9, -96.5, 36.1},
		},
	})
}

func writeSidecarError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func TestProxy_Constants(t *testing.T) {
	// Lowered 64 → 8 by #185: radarSidecarMaxBody rose to 64 MiB, and 8
	// entries keeps the const block's "hard memory bound" true (8 × 64 MiB =
	// 512 MiB, below the previous 64 × 10 MiB = 640 MiB).
	if radarCacheMaxEntries != 8 {
		t.Errorf("radarCacheMaxEntries = %d, want 8", radarCacheMaxEntries)
	}
	if radarSidecarMaxBody != 64<<20 {
		t.Errorf("radarSidecarMaxBody = %d, want %d", radarSidecarMaxBody, 64<<20)
	}
	if radarCacheTTL != 5*time.Minute {
		t.Errorf("radarCacheTTL = %v, want %v", radarCacheTTL, 5*time.Minute)
	}
}

func TestProxy_FetchesAndCaches(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sidecarFeatureCollection(w, r.URL.Query().Get("site"), r.URL.Query().Get("product"))
	}))
	defer srv.Close()

	p := NewProxy(srv.URL)
	ctx := t.Context()

	body1, meta1, err := p.Get(ctx, "TLX", "N0B")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	body2, meta2, err := p.Get(ctx, "TLX", "N0B")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("sidecar hits = %d, want 1 (second Get should be served from cache)", got)
	}
	if string(body1) != string(body2) {
		t.Errorf("cached body mismatch:\n%s\nvs\n%s", body1, body2)
	}
	if meta1.Site != "TLX" || meta1.Product != "N0B" {
		t.Errorf("meta1 = %+v, want site=TLX product=N0B", meta1)
	}
	if !meta1.ScanTime.Equal(meta2.ScanTime) {
		t.Errorf("scan_time mismatch: %v vs %v", meta1.ScanTime, meta2.ScanTime)
	}
}

func TestProxy_N0BFallsBackToN0Q(t *testing.T) {
	var n0bHits, n0qHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("product") {
		case "N0B":
			n0bHits.Add(1)
			writeSidecarError(w, http.StatusServiceUnavailable, "no_recent_scan")
		case "N0Q":
			n0qHits.Add(1)
			sidecarFeatureCollection(w, "TLX", "N0Q")
		}
	}))
	defer srv.Close()

	p := NewProxy(srv.URL)
	_, meta, err := p.Get(t.Context(), "TLX", "N0B")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if meta.Product != "N0Q" {
		t.Errorf("meta.Product = %q, want N0Q (fallback)", meta.Product)
	}
	if n0bHits.Load() != 1 {
		t.Errorf("N0B hits = %d, want 1", n0bHits.Load())
	}
	if n0qHits.Load() != 1 {
		t.Errorf("N0Q hits = %d, want 1", n0qHits.Load())
	}
}

func TestProxy_ErrorEnvelope(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		writeSidecarError(w, http.StatusBadGateway, "decode_failed")
	}))
	defer srv.Close()

	p := NewProxy(srv.URL)
	_, _, err := p.Get(t.Context(), "TLX", "N0B")
	if !errors.Is(err, ErrDecodeFailed) {
		t.Errorf("Get error = %v, want errors.Is ErrDecodeFailed", err)
	}
	// decode_failed is not no_recent_scan, so no N0B->N0Q fallback attempt.
	if hits.Load() != 1 {
		t.Errorf("sidecar hits = %d, want 1 (no fallback for decode_failed)", hits.Load())
	}
}

// paddedSidecarBody writes a valid Contract A success envelope padded to
// exactly total bytes. The padding lives in an unknown string field, which
// sidecarSuccessBody ignores — so the body stays DECODABLE at any size.
// Arbitrary filler bytes would fail on json.Unmarshal instead of proving
// passthrough, which is the distinction #185 turns on.
func paddedSidecarBody(t *testing.T, total int) []byte {
	t.Helper()
	env := map[string]any{
		"type":     "FeatureCollection",
		"features": []any{},
		"metadata": map[string]any{
			"site":      "TLX",
			"product":   "N0B",
			"scan_time": "2026-07-20T12:00:00Z",
			"bbox":      []float64{-98.5, 34.9, -96.5, 36.1},
		},
		"pad": "",
	}
	base, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	padLen := total - len(base)
	if padLen < 0 {
		t.Fatalf("total %d smaller than the envelope itself (%d)", total, len(base))
	}
	env["pad"] = strings.Repeat("x", padLen)
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal padded envelope: %v", err)
	}
	if len(out) != total {
		t.Fatalf("padded body = %d bytes, want exactly %d", len(out), total)
	}
	return out
}

// TestProxy_PayloadJustUnderCapPassesThrough is #185's (a) case: the largest
// payload the proxy accepts must round-trip intact, not be truncated.
// Live reference: a real N0B scan measured 19,471,933 bytes in storm
// conditions, which the old 10 MiB cap silently truncated into a JSON decode
// error surfaced as a 502.
func TestProxy_PayloadJustUnderCapPassesThrough(t *testing.T) {
	body := paddedSidecarBody(t, radarSidecarMaxBody)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, meta, err := NewProxy(srv.URL).Get(t.Context(), "TLX", "N0B")
	if err != nil {
		t.Fatalf("Get at exactly the cap (%d bytes): %v", radarSidecarMaxBody, err)
	}
	if len(got) != len(body) {
		t.Errorf("body round-tripped as %d bytes, want %d — payload was truncated", len(got), len(body))
	}
	if meta.Site != "TLX" || meta.Product != "N0B" {
		t.Errorf("metadata = %+v, want site=TLX product=N0B", meta)
	}
}

// TestProxy_PayloadOverCapFailsLoud is #185's (b) case. One byte over the
// cap must produce an explicit too-large error — NOT a JSON decode error.
// The old code truncated at the limit and handed the fragment to
// json.Unmarshal, so operators saw "unexpected end of JSON input" and a 502
// with no hint that a size limit was the cause.
func TestProxy_PayloadOverCapFailsLoud(t *testing.T) {
	body := paddedSidecarBody(t, radarSidecarMaxBody+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	_, _, err := NewProxy(srv.URL).Get(t.Context(), "TLX", "N0B")
	if err == nil {
		t.Fatal("Get one byte over the cap returned no error")
	}
	if !strings.Contains(err.Error(), "payload exceeds") {
		t.Errorf("error = %q, want it to contain \"payload exceeds\"", err)
	}
	// The regression this closes: the failure must not masquerade as bad JSON.
	for _, decodeish := range []string{"unexpected end of JSON", "decode radar sidecar response"} {
		if strings.Contains(err.Error(), decodeish) {
			t.Errorf("error = %q — that is a DECODE error, not a size error (#185's whole point)", err)
		}
	}
}
