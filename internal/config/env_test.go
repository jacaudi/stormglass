package config

import (
	"strings"
	"testing"
	"time"
)

// envOf returns a getenv backed by a fixed map, so each case is hermetic.
func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestPositiveIntEnv(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "unset takes the default", value: "", want: 100},
		{name: "positive", value: "7", want: 7},
		{name: "unparseable", value: "abc", wantErr: true},
		{name: "zero", value: "0", wantErr: true},
		{name: "negative", value: "-1", wantErr: true},
		{name: "duration is not an int", value: "5s", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PositiveIntEnv(envOf(map[string]string{"KNOB": tt.value}), "KNOB", 100)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("PositiveIntEnv(%q) = %d, nil; want error", tt.value, got)
				}
				for _, want := range []string{"KNOB", `"` + tt.value + `"`} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name %s", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("PositiveIntEnv(%q) unexpected error: %v", tt.value, err)
			}
			if got != tt.want {
				t.Errorf("PositiveIntEnv(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestPositiveDurationEnv(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "unset takes the default", value: "", want: 10 * time.Second},
		{name: "positive", value: "2s", want: 2 * time.Second},
		{name: "unparseable", value: "soon", wantErr: true},
		{name: "bare number has no unit", value: "5", wantErr: true},
		{name: "zero", value: "0s", wantErr: true},
		{name: "negative", value: "-1m", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PositiveDurationEnv(envOf(map[string]string{"KNOB": tt.value}), "KNOB", 10*time.Second)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("PositiveDurationEnv(%q) = %v, nil; want error", tt.value, got)
				}
				for _, want := range []string{"KNOB", `"` + tt.value + `"`} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name %s", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("PositiveDurationEnv(%q) unexpected error: %v", tt.value, err)
			}
			if got != tt.want {
				t.Errorf("PositiveDurationEnv(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseBoolEnv(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		unset   bool
		want    bool
		wantErr bool
	}{
		{name: "unset key", key: "PARSE_BOOL_ENV_TEST_UNSET", unset: true, want: false, wantErr: false},
		{name: "true", key: "PARSE_BOOL_ENV_TEST_TRUE", value: "true", want: true, wantErr: false},
		{name: "1", key: "PARSE_BOOL_ENV_TEST_ONE", value: "1", want: true, wantErr: false},
		{name: "yes is invalid", key: "PARSE_BOOL_ENV_TEST_YES", value: "yes", want: false, wantErr: true},
		{name: "nonsense is invalid", key: "PARSE_BOOL_ENV_TEST_NONSENSE", value: "nonsense", want: false, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.unset {
				t.Setenv(tt.key, tt.value)
			}

			got, err := ParseBoolEnv(tt.key)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseBoolEnv(%q) = %v, nil; want error", tt.key, got)
				}
				if !strings.Contains(err.Error(), tt.key) {
					t.Errorf("ParseBoolEnv(%q) error %q does not name the key", tt.key, err.Error())
				}
				if !strings.Contains(err.Error(), tt.value) {
					t.Errorf("ParseBoolEnv(%q) error %q does not name the offending value %q", tt.key, err.Error(), tt.value)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseBoolEnv(%q) unexpected error: %v", tt.key, err)
			}
			if got != tt.want {
				t.Errorf("ParseBoolEnv(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
