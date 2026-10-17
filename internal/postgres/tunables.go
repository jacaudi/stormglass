package postgres

import (
	"errors"
	"time"

	"github.com/jacaudi/stormglass/internal/config"
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
// injected so tests can supply a fake instead of the real os.Getenv. Unset or
// empty takes the default (100, 10s, 3); a value that does not parse or is not
// positive is an error, and every offender is joined into the one returned.
func postgresTunables(getenv func(string) string) (tunables, error) {
	batch, batchErr := config.PositiveIntEnv(getenv, "POSTGRES_BATCH_SIZE", 100)
	flush, flushErr := config.PositiveDurationEnv(getenv, "POSTGRES_FLUSH_INTERVAL", 10*time.Second)
	retries, retriesErr := config.PositiveIntEnv(getenv, "POSTGRES_MAX_RETRIES", 3)
	return tunables{
		batchSize:     batch,
		flushInterval: flush,
		maxRetries:    retries,
	}, errors.Join(batchErr, flushErr, retriesErr)
}
