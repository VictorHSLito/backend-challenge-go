// Package config loads and validates the service configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/fx"
)

// Module provides a validated Config.
var Module = fx.Module("config", fx.Provide(Load))

type Config struct {
	InstanceID      string
	HTTPAddr        string
	ShutdownTimeout time.Duration
	LogLevel        slog.Level
	DatabaseURL     string
	DBMaxConns      int32
	OIDC            OIDCConfig
	SQS             SQSConfig
	Outbox          OutboxConfig
	Reference       ReferenceConfig
	Failpoint       string
}

type OIDCConfig struct {
	IssuerURL string
	JWKSURL   string
	Audience  string
}

type SQSConfig struct {
	Region            string
	Endpoint          string
	InputQueue        string
	EventsQueue       string
	ConsumerName      string
	ConsumerWorkers   int
	WaitTime          time.Duration
	VisibilityTimeout time.Duration
	MaxReceiveCount   int
	Disabled          bool
}

type OutboxConfig struct {
	PollInterval time.Duration
	BatchSize    int
	Lease        time.Duration
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration
	Disabled     bool
}

type ReferenceConfig struct {
	PollInterval time.Duration
	BatchSize    int
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration
	MaxAttempts  int
	TTL          time.Duration
	Disabled     bool
}

// Load reads the configuration from environment variables and validates it.
func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom reads the configuration using lookup, which makes it testable without touching the process environment.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	r := reader{lookup: lookup}
	host, _ := os.Hostname()

	cfg := Config{
		InstanceID:      r.str("INSTANCE_ID", host),
		HTTPAddr:        r.str("HTTP_ADDR", ":8080"),
		ShutdownTimeout: r.duration("SHUTDOWN_TIMEOUT", 20*time.Second),
		LogLevel:        r.level("LOG_LEVEL", slog.LevelInfo),
		DatabaseURL:     r.str("DATABASE_URL", ""),
		DBMaxConns:      int32(r.int("DB_MAX_CONNS", 20)),
		OIDC: OIDCConfig{
			IssuerURL: r.str("OIDC_ISSUER_URL", ""),
			JWKSURL:   r.str("OIDC_JWKS_URL", ""),
			Audience:  r.str("OIDC_AUDIENCE", "wager-api"),
		},
		SQS: SQSConfig{
			Region:            r.str("AWS_REGION", "us-east-1"),
			Endpoint:          r.str("SQS_ENDPOINT", ""),
			InputQueue:        r.str("SQS_INPUT_QUEUE", "wager-transactions.fifo"),
			EventsQueue:       r.str("SQS_EVENTS_QUEUE", "wallet-events.fifo"),
			ConsumerName:      r.str("SQS_CONSUMER_NAME", "wager-transactions-consumer"),
			ConsumerWorkers:   r.int("SQS_CONSUMER_WORKERS", 4),
			WaitTime:          r.duration("SQS_WAIT_TIME", 10*time.Second),
			VisibilityTimeout: r.duration("SQS_VISIBILITY_TIMEOUT", 30*time.Second),
			MaxReceiveCount:   r.int("SQS_MAX_RECEIVE_COUNT", 5),
			Disabled:          r.bool("SQS_CONSUMER_DISABLED", false),
		},
		Outbox: OutboxConfig{
			PollInterval: r.duration("OUTBOX_POLL_INTERVAL", 500*time.Millisecond),
			BatchSize:    r.int("OUTBOX_BATCH_SIZE", 50),
			Lease:        r.duration("OUTBOX_LEASE", 30*time.Second),
			BaseBackoff:  r.duration("OUTBOX_BASE_BACKOFF", time.Second),
			MaxBackoff:   r.duration("OUTBOX_MAX_BACKOFF", 5*time.Minute),
			Disabled:     r.bool("OUTBOX_DISABLED", false),
		},
		Reference: ReferenceConfig{
			PollInterval: r.duration("REFERENCE_POLL_INTERVAL", time.Second),
			BatchSize:    r.int("REFERENCE_BATCH_SIZE", 50),
			BaseBackoff:  r.duration("REFERENCE_BASE_BACKOFF", 2*time.Second),
			MaxBackoff:   r.duration("REFERENCE_MAX_BACKOFF", 2*time.Minute),
			MaxAttempts:  r.int("REFERENCE_MAX_ATTEMPTS", 8),
			TTL:          r.duration("REFERENCE_TTL", 15*time.Minute),
			Disabled:     r.bool("REFERENCE_WORKER_DISABLED", false),
		},
		Failpoint: r.str("FAILPOINT", ""),
	}

	if err := errors.Join(append(r.errs, cfg.Validate())...); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// Validate reports every invalid setting at once.
func (c Config) Validate() error {
	var errs []error
	req := func(name, v string) {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	positive := func(name string, v int64) {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive", name))
		}
	}
	absURL := func(name, v string) {
		if v == "" {
			return
		}
		if u, err := url.Parse(v); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s must be an absolute URL", name))
		}
	}

	req("HTTP_ADDR", c.HTTPAddr)
	req("DATABASE_URL", c.DatabaseURL)
	req("OIDC_ISSUER_URL", c.OIDC.IssuerURL)
	req("OIDC_JWKS_URL", c.OIDC.JWKSURL)
	req("OIDC_AUDIENCE", c.OIDC.Audience)
	req("AWS_REGION", c.SQS.Region)
	req("SQS_INPUT_QUEUE", c.SQS.InputQueue)
	req("SQS_EVENTS_QUEUE", c.SQS.EventsQueue)
	req("SQS_CONSUMER_NAME", c.SQS.ConsumerName)
	absURL("OIDC_ISSUER_URL", c.OIDC.IssuerURL)
	absURL("OIDC_JWKS_URL", c.OIDC.JWKSURL)
	absURL("SQS_ENDPOINT", c.SQS.Endpoint)

	positive("SHUTDOWN_TIMEOUT", int64(c.ShutdownTimeout))
	positive("DB_MAX_CONNS", int64(c.DBMaxConns))
	positive("SQS_CONSUMER_WORKERS", int64(c.SQS.ConsumerWorkers))
	positive("SQS_VISIBILITY_TIMEOUT", int64(c.SQS.VisibilityTimeout))
	positive("SQS_MAX_RECEIVE_COUNT", int64(c.SQS.MaxReceiveCount))
	positive("OUTBOX_POLL_INTERVAL", int64(c.Outbox.PollInterval))
	positive("OUTBOX_BATCH_SIZE", int64(c.Outbox.BatchSize))
	positive("OUTBOX_LEASE", int64(c.Outbox.Lease))
	positive("OUTBOX_BASE_BACKOFF", int64(c.Outbox.BaseBackoff))
	positive("REFERENCE_POLL_INTERVAL", int64(c.Reference.PollInterval))
	positive("REFERENCE_BATCH_SIZE", int64(c.Reference.BatchSize))
	positive("REFERENCE_BASE_BACKOFF", int64(c.Reference.BaseBackoff))
	positive("REFERENCE_MAX_ATTEMPTS", int64(c.Reference.MaxAttempts))
	positive("REFERENCE_TTL", int64(c.Reference.TTL))

	if c.SQS.WaitTime < 0 || c.SQS.WaitTime > 20*time.Second {
		errs = append(errs, errors.New("SQS_WAIT_TIME must be between 0s and 20s"))
	}
	if c.Outbox.MaxBackoff < c.Outbox.BaseBackoff {
		errs = append(errs, errors.New("OUTBOX_MAX_BACKOFF must be >= OUTBOX_BASE_BACKOFF"))
	}
	if c.Reference.MaxBackoff < c.Reference.BaseBackoff {
		errs = append(errs, errors.New("REFERENCE_MAX_BACKOFF must be >= REFERENCE_BASE_BACKOFF"))
	}
	return errors.Join(errs...)
}

type reader struct {
	lookup func(string) (string, bool)
	errs   []error
}

func (r *reader) str(key, def string) string {
	if v, ok := r.lookup(key); ok && v != "" {
		return v
	}
	return def
}

func (r *reader) int(key string, def int) int {
	v, ok := r.lookup(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: invalid integer %q", key, v))
		return def
	}
	return n
}

func (r *reader) bool(key string, def bool) bool {
	v, ok := r.lookup(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: invalid boolean %q", key, v))
		return def
	}
	return b
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	v, ok := r.lookup(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: invalid duration %q", key, v))
		return def
	}
	return d
}

func (r *reader) level(key string, def slog.Level) slog.Level {
	v, ok := r.lookup(key)
	if !ok || v == "" {
		return def
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(v)); err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: invalid log level %q", key, v))
		return def
	}
	return l
}
