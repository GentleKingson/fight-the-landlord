package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

const redactedValue = "[REDACTED]"

// NewLogger builds a structured server logger with defense-in-depth redaction
// for credential-like attributes. Callers must still avoid logging payloads.
func NewLogger(format string, writer io.Writer) (*slog.Logger, error) {
	if writer == nil {
		return nil, fmt.Errorf("log writer is required")
	}
	options := &slog.HandlerOptions{Level: slog.LevelInfo}
	var handler slog.Handler
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		handler = slog.NewJSONHandler(writer, options)
	case "text":
		handler = slog.NewTextHandler(writer, options)
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}
	return slog.New(&redactingHandler{next: handler}).With("service", "fight-the-landlord"), nil
}

// ConfigureDefaultLogger installs the server logger and makes legacy log.Printf
// calls use the same JSON/text encoding while they are migrated to slog fields.
func ConfigureDefaultLogger(format string, writer io.Writer) (*slog.Logger, error) {
	logger, err := NewLogger(format, writer)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(logger)
	return logger, nil
}

type redactingHandler struct {
	next slog.Handler
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for index, attr := range attrs {
		clean[index] = redactAttr(attr)
	}
	return &redactingHandler{next: h.next.WithAttrs(clean)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

func redactAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if sensitiveLogKey(attr.Key) {
		return slog.String(attr.Key, redactedValue)
	}
	if attr.Value.Kind() == slog.KindGroup {
		children := attr.Value.Group()
		clean := make([]slog.Attr, len(children))
		for index, child := range children {
			clean[index] = redactAttr(child)
		}
		return slog.Group(attr.Key, attrsToAny(clean)...)
	}
	return attr
}

func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, len(attrs))
	for index, attr := range attrs {
		values[index] = attr
	}
	return values
}

func sensitiveLogKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(key))
	for _, marker := range []string{"token", "ticket", "password", "cookie", "authorization", "secret", "chat_body", "message_body"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
