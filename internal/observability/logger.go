// Package observability configures logs, metrics, and traces for the API.
package observability

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/trace"
)

// SetupLogger installs a JSON logger, or a text logger when LOG_FORMAT=text.
// Log records include trace_id and span_id when a span is on the context.
func SetupLogger() {
	level := slog.LevelInfo

	var base slog.Handler

	if os.Getenv("LOG_FORMAT") == "text" {
		level = slog.LevelDebug
		base = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		base = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}

	slog.SetDefault(slog.New(fanout{
		traceHandler{next: base},
		otelslog.NewHandler("futureco-schedule"),
	}))
}

type traceHandler struct {
	next slog.Handler
}

func (h traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		record = record.Clone()
		record.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}

	return h.next.Handle(ctx, record)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{next: h.next.WithGroup(name)}
}

// fanout delivers one record to each handler. Go 1.23 has no slog.NewMultiHandler.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range f {
		if handler.Enabled(ctx, level) {
			return true
		}
	}

	return false
}

func (f fanout) Handle(ctx context.Context, record slog.Record) error {
	var first error

	for _, handler := range f {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}

		if err := handler.Handle(ctx, record.Clone()); err != nil && first == nil {
			first = err
		}
	}

	return first
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make(fanout, len(f))
	for i, handler := range f {
		next[i] = handler.WithAttrs(attrs)
	}

	return next
}

func (f fanout) WithGroup(name string) slog.Handler {
	next := make(fanout, len(f))
	for i, handler := range f {
		next[i] = handler.WithGroup(name)
	}

	return next
}
