package radar

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	// radarCacheMaxEntries bounds the LRU. ~160 sites but any one deployment
	// tracks 1 station → a handful of sites × 2 products (N0B/N0Q) × the last
	// few scan times; 64 is generous headroom and a hard memory bound.
	radarCacheMaxEntries = 64
	// radarCacheTTL ≈ one WSR-88D volume-scan cycle (VCP ~4–6 min); 5 min keeps
	// a scan cached for its useful life without serving stale reflectivity.
	radarCacheTTL = 5 * time.Minute
	// radarSidecarTimeout bounds the HTTP call to the Python sidecar.
	radarSidecarTimeout = 30 * time.Second
)

// Typed sidecar error codes (Contract A), exported so the HTTP layer (Task
// 2.5) can branch on them with errors.Is. Wrapped errors returned from
// fetch/Get remain errors.Is-checkable against these sentinels.
var (
	ErrNoRecentScan = errors.New("radar: sidecar has no recent scan for this site")
	ErrDecodeFailed = errors.New("radar: sidecar failed to decode the scan")
	ErrInternal     = errors.New("radar: sidecar internal error")
)

// Metadata is the top-level `metadata` object Contract A embeds in every
// successful GeoJSON FeatureCollection response.
type Metadata struct {
	Site     string    `json:"site"`
	Product  string    `json:"product"`
	ScanTime time.Time `json:"scan_time"`
	BBox     []float64 `json:"bbox"`
}

// cacheKey identifies a cached scan by the two inputs known at call time.
// scanTime (the cached scan's logical identity, per the design doc's
// "(site,product,scanTime)" phrasing) is not known until after a fetch, so
// it lives in the cached value rather than the key -- see cacheEntry.
type cacheKey struct {
	site    string
	product string
}

// cacheEntry is the LRU's cached value: the decoded scan's identity
// (scanTime), the raw response body, and storedAt -- the wall-clock time
// this entry was cached, which drives TTL expiry.
type cacheEntry struct {
	key      cacheKey
	body     json.RawMessage
	meta     Metadata
	storedAt time.Time
}

// Proxy is an HTTP client to the Python radar sidecar (Contract A), with a
// bounded LRU cache and N0B→N0Q fallback.
type Proxy struct {
	sidecarURL string
	http       *http.Client

	mu    sync.Mutex
	order *list.List // *cacheEntry, front = most recently used
	index map[cacheKey]*list.Element
}

// NewProxy creates a Proxy that calls the radar sidecar at sidecarURL.
func NewProxy(sidecarURL string) *Proxy {
	return &Proxy{
		sidecarURL: sidecarURL,
		http:       &http.Client{Timeout: radarSidecarTimeout},
		order:      list.New(),
		index:      make(map[cacheKey]*list.Element),
	}
}

// Get returns the GeoJSON FeatureCollection body and its metadata for
// (site, product), serving from cache when a fresh entry exists. On a
// 503 no_recent_scan for product "N0B", it retries once with "N0Q" (O2
// default+fallback); "N0Q" itself never falls back further.
func (p *Proxy) Get(ctx context.Context, site, product string) (json.RawMessage, Metadata, error) {
	key := cacheKey{site: site, product: product}

	if body, meta, ok := p.cached(key); ok {
		return body, meta, nil
	}

	body, meta, err := p.fetch(ctx, site, product)
	if err != nil {
		if product == "N0B" && errors.Is(err, ErrNoRecentScan) {
			return p.Get(ctx, site, "N0Q")
		}
		return nil, Metadata{}, err
	}

	p.store(key, body, meta)
	return body, meta, nil
}

// cached returns the cached (body, meta) for key if present and within
// radarCacheTTL of storedAt, promoting it to most-recently-used. A stale
// entry is evicted and reported as a miss.
func (p *Proxy) cached(key cacheKey) (json.RawMessage, Metadata, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	elem, ok := p.index[key]
	if !ok {
		return nil, Metadata{}, false
	}
	entry := elem.Value.(*cacheEntry)
	if time.Since(entry.storedAt) >= radarCacheTTL {
		p.order.Remove(elem)
		delete(p.index, key)
		return nil, Metadata{}, false
	}
	p.order.MoveToFront(elem)
	return entry.body, entry.meta, true
}

// store caches (body, meta) under key, evicting the least-recently-used
// entry once the cache exceeds radarCacheMaxEntries. Only successful
// fetches are cached -- error outcomes (e.g. a transient no_recent_scan)
// are never cached, since sidecar availability can change scan-to-scan.
func (p *Proxy) store(key cacheKey, body json.RawMessage, meta Metadata) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if elem, ok := p.index[key]; ok {
		p.order.Remove(elem)
		delete(p.index, key)
	}

	elem := p.order.PushFront(&cacheEntry{key: key, body: body, meta: meta, storedAt: time.Now()})
	p.index[key] = elem

	for p.order.Len() > radarCacheMaxEntries {
		oldest := p.order.Back()
		if oldest == nil {
			break
		}
		p.order.Remove(oldest)
		delete(p.index, oldest.Value.(*cacheEntry).key)
	}
}

// sidecarErrorEnvelope is Contract A's non-200 error body: {"error": "<code>"}.
type sidecarErrorEnvelope struct {
	Error string `json:"error"`
}

// sidecarSuccessBody is the subset of Contract A's 200 GeoJSON
// FeatureCollection this client needs to decode; the full raw body is
// returned unchanged so callers (Task 2.5) can write it straight through.
type sidecarSuccessBody struct {
	Metadata Metadata `json:"metadata"`
}

// fetch performs one uncached GET against the sidecar for (site, product)
// and maps a non-200 response to a typed sentinel error.
func (p *Proxy) fetch(ctx context.Context, site, product string) (json.RawMessage, Metadata, error) {
	reqURL := p.sidecarURL + "/radar?" + url.Values{"site": {site}, "product": {product}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, Metadata{}, fmt.Errorf("build radar sidecar request: %w", err)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, Metadata{}, scrubTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, Metadata{}, fmt.Errorf("read radar sidecar response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var envelope sidecarErrorEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, Metadata{}, fmt.Errorf("%w: decode error envelope (status %d): %w", ErrInternal, resp.StatusCode, err)
		}
		return nil, Metadata{}, mapSidecarError(envelope.Error, resp.StatusCode)
	}

	var wrapper sidecarSuccessBody
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, Metadata{}, fmt.Errorf("decode radar sidecar response: %w", err)
	}

	return json.RawMessage(body), wrapper.Metadata, nil
}

// mapSidecarError maps a Contract A error code to its typed sentinel. An
// unrecognized code is wrapped as ErrInternal rather than dropped, so
// callers can still branch on errors.Is(err, ErrInternal) and the original
// code and status are preserved in the error text for logs.
func mapSidecarError(code string, status int) error {
	switch code {
	case "no_recent_scan":
		return ErrNoRecentScan
	case "decode_failed":
		return ErrDecodeFailed
	case "internal":
		return ErrInternal
	default:
		return fmt.Errorf("%w: unrecognized sidecar error code %q (status %d)", ErrInternal, code, status)
	}
}

// scrubTransportError returns a URL-free error for a transport failure. A
// failed *http.Client.Do commonly returns a *url.Error whose Error() string
// embeds the full request URL; that URL (the sidecar's address) is scrubbed
// defensively so it never reaches a log line.
func scrubTransportError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return errors.New("radar sidecar request failed: upstream unreachable")
	}
	return fmt.Errorf("radar sidecar request failed: %w", err)
}
