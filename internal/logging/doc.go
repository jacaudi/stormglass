// Package logging owns the process's logging policy: decoding LOG_LEVEL and
// LOG_FORMAT, building the handler they describe, installing it as slog's
// default, and the tee that fans records out to more than one handler. It
// never exits the process; what a bad logging configuration costs is the
// caller's decision.
package logging
