// Package observability provides structured logging and Prometheus metrics.
package observability

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/victorhslito/junglegaming/internal/config"
)

// Log field names shared by every component so operations can be traced end to end.
const (
	FieldCorrelationID = "correlationId"
	FieldMessageID     = "messageId"
	FieldTransactionID = "transactionId"
	FieldWalletID      = "walletId"
	FieldProviderID    = "providerId"
)

type ctxKey struct{}

type correlationKey struct{}

// WithCorrelationID stores the correlation id for logs and for events created in this context.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	ctx = context.WithValue(ctx, correlationKey{}, id)
	return WithLogAttrs(ctx, slog.String(FieldCorrelationID, id))
}

// CorrelationID returns the correlation id stored in ctx, or "".
func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}

// WithLogAttrs returns a context whose log records will include attrs.
// Later values for the same key override earlier ones.
func WithLogAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	merged := make([]slog.Attr, 0, len(prev)+len(attrs))
	for _, p := range prev {
		if !containsKey(attrs, p.Key) {
			merged = append(merged, p)
		}
	}
	merged = append(merged, attrs...)
	return context.WithValue(ctx, ctxKey{}, merged)
}

// LogAttrs returns the attributes stored in ctx.
func LogAttrs(ctx context.Context) []slog.Attr {
	attrs, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	return attrs
}

func containsKey(attrs []slog.Attr, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

// NewLogger builds the JSON logger used by the whole application.
func NewLogger(cfg config.Config) *slog.Logger {
	return NewLoggerTo(os.Stdout, cfg.LogLevel, cfg.InstanceID)
}

// NewLoggerTo builds a JSON logger writing to w; used by tests.
func NewLoggerTo(w io.Writer, level slog.Level, instanceID string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(contextHandler{Handler: h}).With(slog.String("instance", instanceID))
}

// contextHandler copies the identifiers stored with WithLogAttrs into each record.
type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs := LogAttrs(ctx); len(attrs) > 0 {
		r.AddAttrs(attrs...)
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: h.Handler.WithGroup(name)}
}
