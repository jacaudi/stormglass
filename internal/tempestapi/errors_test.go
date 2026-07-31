package tempestapi

import (
	"errors"
	"fmt"
	"testing"
)

func TestStatusErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *StatusError
		want string
	}{
		{
			name: "api level status code",
			err:  &StatusError{StatusCode: 404, Message: "NOT FOUND"},
			want: "weatherflow status_code 404: NOT FOUND",
		},
		{
			name: "http level status",
			err:  &StatusError{HTTPStatus: 503},
			want: "weatherflow API status 503",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The retry layer classifies with errors.As, so StatusError must survive
// wrapping. NOTE: errors.As, never errors.AsType — that is Go 1.26 and
// go.mod declares go 1.25.0.
func TestStatusErrorUnwrapsThroughFmtErrorf(t *testing.T) {
	wrapped := fmt.Errorf("fetch window: %w", &StatusError{HTTPStatus: 429})
	var se *StatusError
	if !errors.As(wrapped, &se) {
		t.Fatal("errors.As failed to extract *StatusError from a wrapped error")
	}
	if se.HTTPStatus != 429 {
		t.Errorf("HTTPStatus = %d, want 429", se.HTTPStatus)
	}
}

func TestStationFieldsAreExported(t *testing.T) {
	s := Station{SerialNumber: "ST-00000001", DeviceID: 42}
	if s.SerialNumber != "ST-00000001" || s.DeviceID != 42 {
		t.Error("Station.SerialNumber and Station.DeviceID must be settable from outside the package")
	}
}
