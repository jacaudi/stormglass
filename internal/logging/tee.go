package logging

import (
	"context"
	"errors"
	"log/slog"
)

// tee fans every slog.Record at or above level out to multiple handlers.
type tee struct {
	level    slog.Level
	handlers []slog.Handler
}

// NewTee returns a slog.Handler that forwards every record at or above level
// to each of handlers.
//
// It exists to work around a real stdlib side effect: slog.SetDefault(l) calls
// log.SetOutput(&handlerWriter{l.Handler(), ...}) whenever l.Handler() isn't
// the unexported *defaultHandler type (see log/slog/logger.go's SetDefault doc
// comment), which means installing the OTel log bridge as the sole slog
// default would silently redirect every slog record, and anything a
// dependency still writes through stdlib log, away from stderr into the OTel
// pipeline only. Fanning out to a plain stderr handler alongside the OTel one
// preserves that visibility.
//
// level is LOG_LEVEL. The stderr handler already filters on it, but the OTel
// bridge enables every level, so the tee is where one LOG_LEVEL governs both.
func NewTee(level slog.Level, handlers ...slog.Handler) slog.Handler {
	return &tee{level: level, handlers: handlers}
}

func (h *tee) Enabled(ctx context.Context, level slog.Level) bool {
	if level < h.level {
		return false
	}
	for _, sub := range h.handlers {
		if sub.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *tee) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < h.level {
		return nil
	}
	var errs []error
	for _, sub := range h.handlers {
		if !sub.Enabled(ctx, r.Level) {
			continue
		}
		if err := sub.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *tee) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		next[i] = sub.WithAttrs(attrs)
	}
	return &tee{level: h.level, handlers: next}
}

func (h *tee) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		next[i] = sub.WithGroup(name)
	}
	return &tee{level: h.level, handlers: next}
}
