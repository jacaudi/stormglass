package postgres

import (
	"strings"
	"testing"
	"time"
)

func TestPostgresTunables(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{
			"POSTGRES_BATCH_SIZE":     "7",
			"POSTGRES_FLUSH_INTERVAL": "2s",
			"POSTGRES_MAX_RETRIES":    "5",
		}[k]
	}
	tn, err := postgresTunables(getenv)
	if err != nil {
		t.Fatalf("postgresTunables(valid) error = %v", err)
	}
	if tn.batchSize != 7 || tn.flushInterval != 2*time.Second || tn.maxRetries != 5 {
		t.Fatalf("tunables not applied: %+v", tn)
	}
	// defaults when unset:
	def, err := postgresTunables(func(string) string { return "" })
	if err != nil {
		t.Fatalf("postgresTunables(unset) error = %v", err)
	}
	if def.batchSize != 100 || def.flushInterval != 10*time.Second || def.maxRetries != 3 {
		t.Fatalf("defaults wrong: %+v", def)
	}
	// Malformed is fatal, not a fall back to the default, and every offender
	// is named in the one error.
	badEnv := func(k string) string {
		return map[string]string{
			"POSTGRES_BATCH_SIZE":     "0",
			"POSTGRES_FLUSH_INTERVAL": "abc",
			"POSTGRES_MAX_RETRIES":    "-1",
		}[k]
	}
	_, err = postgresTunables(badEnv)
	if err == nil {
		t.Fatal("postgresTunables(malformed) error = nil, want one naming every offender")
	}
	for _, want := range []string{"POSTGRES_BATCH_SIZE", `"0"`, "POSTGRES_FLUSH_INTERVAL", `"abc"`, "POSTGRES_MAX_RETRIES", `"-1"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// NewPostgresWriter reports a malformed tunable before it touches the
// database: the URL here would fail to parse, so an error naming the tunable
// proves the tunables were checked first.
func TestNewPostgresWriter_MalformedTunableFailsBeforeConnecting(t *testing.T) {
	t.Setenv("POSTGRES_BATCH_SIZE", "abc")

	_, err := NewPostgresWriter(t.Context(), "not-a-valid-url")
	if err == nil || !strings.Contains(err.Error(), "POSTGRES_BATCH_SIZE") {
		t.Fatalf("NewPostgresWriter error = %v, want one naming POSTGRES_BATCH_SIZE", err)
	}
}
