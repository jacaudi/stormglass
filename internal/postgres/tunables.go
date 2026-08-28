package postgres

import (
	"strconv"
	"time"
)

// tunables holds the batching/retry knobs documented in docs/configuration.md
// (POSTGRES_BATCH_SIZE, POSTGRES_FLUSH_INTERVAL, POSTGRES_MAX_RETRIES) so
// NewPostgresWriter can wire them from the environment instead of the
// previously-hardcoded, inert defaults (C-H2).
type tunables struct {
	batchSize     int
	flushInterval time.Duration
	maxRetries    int
}

// postgresTunables is a pure helper (testable without a live DB): getenv is
// injected so tests can supply a fake instead of the real os.Getenv. Each
// value falls back to its default when unset, unparseable, or non-positive.
func postgresTunables(getenv func(string) string) tunables {
	atoiOr := func(s string, def int) int {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
		return def
	}
	durOr := func(s string, def time.Duration) time.Duration {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
		return def
	}
	return tunables{
		batchSize:     atoiOr(getenv("POSTGRES_BATCH_SIZE"), 100),
		flushInterval: durOr(getenv("POSTGRES_FLUSH_INTERVAL"), 10*time.Second),
		maxRetries:    atoiOr(getenv("POSTGRES_MAX_RETRIES"), 3),
	}
}
