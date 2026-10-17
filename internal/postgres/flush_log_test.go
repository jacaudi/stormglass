package postgres

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
)

// flushWithRetry's log records are the only operator-visible trace of rows that
// never reached Postgres, so their level and attributes are a contract: an
// attempt that failed is a WARN, the decision to drop the batch is an ERROR, and
// both carry the table and row count as attributes rather than baked into the
// message (where a log pipeline cannot filter or sum them).
func TestFlushWithRetry_LogsDroppedBatchWithStructuredAttrs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	w := &PostgresWriter{maxRetries: 3}
	// A plain error is not retryable (isRetryable is fail-closed), so the batch
	// is dropped on the first attempt.
	w.flushWithRetry(t.Context(), func() error { return errors.New("permanent failure") }, "observations", 7)

	var records []map[string]any
	for dec := json.NewDecoder(&buf); dec.More(); {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("log output is not JSON records: %v\n%s", err, buf.String())
		}
		records = append(records, rec)
	}

	want := []struct{ level, msg string }{
		{"WARN", "postgres: failed to write rows"},
		{"ERROR", "postgres: non-retryable error, dropping batch"},
	}
	if len(records) != len(want) {
		t.Fatalf("got %d log records, want %d:\n%s", len(records), len(want), buf.String())
	}
	for i, exp := range want {
		rec := records[i]
		if rec["level"] != exp.level || rec["msg"] != exp.msg {
			t.Errorf("record %d = level %v msg %q, want level %s msg %q", i, rec["level"], rec["msg"], exp.level, exp.msg)
		}
		if rec["table"] != "observations" || rec["rows"] != float64(7) {
			t.Errorf("record %d table/rows = %v/%v, want observations/7", i, rec["table"], rec["rows"])
		}
	}
	if records[0]["attempt"] != float64(1) || records[0]["max_attempts"] != float64(3) {
		t.Errorf("attempt record = attempt %v of %v, want 1 of 3", records[0]["attempt"], records[0]["max_attempts"])
	}
}
