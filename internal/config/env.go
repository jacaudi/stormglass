package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// ParseBoolEnv reads the named environment variable and parses it as a
// boolean using strconv.ParseBool. An unset or empty variable is treated as
// false with no error. A value that strconv.ParseBool cannot parse (e.g. a
// typo like "ture") is a fatal configuration error rather than a silently
// disabled feature: the returned error names both the variable and the
// offending value.
func ParseBoolEnv(key string) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return false, nil
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value %q for %s: %w", v, key, err)
	}
	return b, nil
}

// PositiveIntEnv reads key via getenv as a strictly positive integer. Unset
// or empty yields def with no error; a value that does not parse, or is zero
// or negative, is an error naming both the variable and the value. getenv is
// injected so callers' decoders can be tested without the process
// environment.
func PositiveIntEnv(getenv func(string) string, key string, def int) (int, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid integer value %q for %s: %w", v, key, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("value %q for %s must be positive", v, key)
	}
	return n, nil
}

// PositiveDurationEnv is PositiveIntEnv for a Go duration string
// (time.ParseDuration), e.g. "10s".
func PositiveDurationEnv(getenv func(string) string, key string, def time.Duration) (time.Duration, error) {
	v := getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid duration value %q for %s: %w", v, key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("value %q for %s must be positive", v, key)
	}
	return d, nil
}
