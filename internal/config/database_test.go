package config

import (
	"net/url"
	"testing"
)

func TestGetDatabaseConfig_FullURL(t *testing.T) {
	// Set full connection string
	t.Setenv("POSTGRES_URL", "postgresql://user:pass@localhost:5432/testdb")

	dsn, err := GetDatabaseConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "postgresql://user:pass@localhost:5432/testdb"
	if dsn != expected {
		t.Errorf("got %q, want %q", dsn, expected)
	}
}

func TestGetDatabaseConfig_Components(t *testing.T) {
	// Clear POSTGRES_URL
	t.Setenv("POSTGRES_URL", "")

	// Set individual components
	t.Setenv("POSTGRES_HOST", "postgres")
	t.Setenv("POSTGRES_PORT", "5433")
	t.Setenv("POSTGRES_USERNAME", "tempest")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	t.Setenv("POSTGRES_NAME", "weather")
	t.Setenv("POSTGRES_SSLMODE", "require")

	dsn, err := GetDatabaseConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "postgresql://tempest:secret@postgres:5433/weather?sslmode=require"
	if dsn != expected {
		t.Errorf("got %q, want %q", dsn, expected)
	}
}

func TestGetDatabaseConfig_ComponentDefaults(t *testing.T) {
	t.Setenv("POSTGRES_URL", "")
	t.Setenv("POSTGRES_HOST", "postgres")
	t.Setenv("POSTGRES_USERNAME", "user")
	t.Setenv("POSTGRES_PASSWORD", "pass")
	t.Setenv("POSTGRES_NAME", "db")
	// Don't set PORT or SSLMODE - should use defaults

	dsn, err := GetDatabaseConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "postgresql://user:pass@postgres:5432/db?sslmode=disable"
	if dsn != expected {
		t.Errorf("got %q, want %q", dsn, expected)
	}
}

func TestGetDatabaseConfig_EscapesCredentials(t *testing.T) {
	t.Setenv("POSTGRES_URL", "")

	const specialPassword = "p@ss:w/o?r#d&1"

	t.Setenv("POSTGRES_HOST", "postgres")
	t.Setenv("POSTGRES_USERNAME", "tempest")
	t.Setenv("POSTGRES_PASSWORD", specialPassword)
	t.Setenv("POSTGRES_NAME", "weather")

	dsn, err := GetDatabaseConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN %q did not round-trip through url.Parse: %v", dsn, err)
	}

	got, ok := parsed.User.Password()
	if !ok {
		t.Fatalf("DSN %q has no password component", dsn)
	}
	if got != specialPassword {
		t.Errorf("password round-trip mismatch: got %q, want %q", got, specialPassword)
	}
}

func TestGetDatabaseConfig_NoConfig(t *testing.T) {
	t.Setenv("POSTGRES_URL", "")
	t.Setenv("POSTGRES_HOST", "")

	dsn, err := GetDatabaseConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dsn != "" {
		t.Errorf("expected empty string when no config, got %q", dsn)
	}
}

func TestGetDatabaseConfig_MissingRequired(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
	}{
		{
			name: "missing username",
			envVars: map[string]string{
				"POSTGRES_HOST":     "postgres",
				"POSTGRES_PASSWORD": "pass",
				"POSTGRES_NAME":     "db",
			},
		},
		{
			name: "missing password",
			envVars: map[string]string{
				"POSTGRES_HOST":     "postgres",
				"POSTGRES_USERNAME": "user",
				"POSTGRES_NAME":     "db",
			},
		},
		{
			name: "missing database name",
			envVars: map[string]string{
				"POSTGRES_HOST":     "postgres",
				"POSTGRES_USERNAME": "user",
				"POSTGRES_PASSWORD": "pass",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("POSTGRES_URL", "")

			// Set provided vars
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			_, err := GetDatabaseConfig()
			if err == nil {
				t.Error("expected error for missing required field, got nil")
			}
		})
	}
}
