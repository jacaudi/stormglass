package tempestapi

import "fmt"

// StatusError is a failed Tempest REST call, carrying enough structure for a
// caller to decide whether retrying could help. It exists because three
// behaviors branch on the KIND of failure — retry on 429/5xx/network, treat a
// non-zero API status_code as a real failure, and exit non-zero if any gap
// failed — and string-matching an opaque error is not a classification.
//
// Exactly one of the two codes is meaningful per instance:
//
//   - HTTPStatus != 0: the transport-level response was not 200. Transient
//     for 429 and 5xx.
//   - StatusCode != 0: the response was HTTP 200 but WeatherFlow reported an
//     application-level failure in its status envelope. Never transient.
//
// Classify with errors.As, NOT errors.AsType (Go 1.26; go.mod declares 1.25.0).
type StatusError struct {
	HTTPStatus int    // HTTP response status, 0 when the failure is API-level
	StatusCode int    // WeatherFlow status.status_code, 0 when purely HTTP
	Message    string // WeatherFlow status.status_message, if any
}

func (e *StatusError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("weatherflow status_code %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("weatherflow API status %d", e.HTTPStatus)
}
