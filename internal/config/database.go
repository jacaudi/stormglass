// Package config resolves database connectivity from environment
// variables: a full POSTGRES_URL connection string takes precedence over
// its individual POSTGRES_HOST/PORT/USERNAME/PASSWORD/NAME/SSLMODE
// components, and an unset POSTGRES_HOST means no database is configured
// at all.
package config

import (
	"cmp"
	"fmt"
	"net"
	"net/url"
	"os"
)

// GetDatabaseConfig returns the PostgreSQL connection string.
// It supports two configuration methods with precedence:
// 1. POSTGRES_URL (full connection string) - takes precedence
// 2. Individual components (POSTGRES_HOST, POSTGRES_PORT, etc.)
//
// Returns empty string if no database is configured (POSTGRES_URL and POSTGRES_HOST both unset).
// Returns error if POSTGRES_HOST is set but required fields are missing.
func GetDatabaseConfig() (string, error) {
	// Option 1: Full connection string (takes precedence)
	if dbURL := os.Getenv("POSTGRES_URL"); dbURL != "" {
		return dbURL, nil
	}

	// Option 2: Build from components
	host := os.Getenv("POSTGRES_HOST")
	if host == "" {
		return "", nil // No database configured
	}

	username := os.Getenv("POSTGRES_USERNAME")
	if username == "" {
		return "", fmt.Errorf("POSTGRES_USERNAME required when using POSTGRES_HOST")
	}

	password := os.Getenv("POSTGRES_PASSWORD")
	if password == "" {
		return "", fmt.Errorf("POSTGRES_PASSWORD required when using POSTGRES_HOST")
	}

	dbname := os.Getenv("POSTGRES_NAME")
	if dbname == "" {
		return "", fmt.Errorf("POSTGRES_NAME required when using POSTGRES_HOST")
	}

	port := cmp.Or(os.Getenv("POSTGRES_PORT"), "5432")
	sslmode := cmp.Or(os.Getenv("POSTGRES_SSLMODE"), "disable")

	u := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(username, password), // percent-encodes @ : / ? # &
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + dbname,
	}
	u.RawQuery = url.Values{"sslmode": {sslmode}}.Encode()
	return u.String(), nil
}
