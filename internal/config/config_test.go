package config

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":    "postgres://u:p@localhost:5432/db",
		"OIDC_ISSUER_URL": "http://localhost:8180/realms/wager",
		"OIDC_JWKS_URL":   "http://localhost:8180/realms/wager/protocol/openid-connect/certs",
	}
}

func TestLoadFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr []string
		check   func(*testing.T, Config)
	}{
		{
			name: "defaults",
			check: func(t *testing.T, c Config) {
				if c.HTTPAddr != ":8080" || c.SQS.MaxReceiveCount != 5 || c.Reference.MaxAttempts != 8 {
					t.Fatalf("unexpected defaults: %+v", c)
				}
				if c.LogLevel != slog.LevelInfo {
					t.Fatalf("log level = %v", c.LogLevel)
				}
			},
		},
		{
			name: "overrides",
			mutate: func(m map[string]string) {
				m["LOG_LEVEL"] = "debug"
				m["OUTBOX_LEASE"] = "5s"
				m["SQS_CONSUMER_WORKERS"] = "8"
				m["SQS_CONSUMER_DISABLED"] = "true"
			},
			check: func(t *testing.T, c Config) {
				if c.LogLevel != slog.LevelDebug || c.Outbox.Lease != 5*time.Second ||
					c.SQS.ConsumerWorkers != 8 || !c.SQS.Disabled {
					t.Fatalf("overrides not applied: %+v", c)
				}
			},
		},
		{
			name:    "missing required",
			mutate:  func(m map[string]string) { delete(m, "DATABASE_URL"); delete(m, "OIDC_JWKS_URL") },
			wantErr: []string{"DATABASE_URL is required", "OIDC_JWKS_URL is required"},
		},
		{
			name: "malformed values are all reported",
			mutate: func(m map[string]string) {
				m["OUTBOX_LEASE"] = "soon"
				m["DB_MAX_CONNS"] = "x"
				m["LOG_LEVEL"] = "loud"
			},
			wantErr: []string{"OUTBOX_LEASE", "DB_MAX_CONNS", "LOG_LEVEL"},
		},
		{
			name:    "relative url",
			mutate:  func(m map[string]string) { m["OIDC_ISSUER_URL"] = "/realms/wager" },
			wantErr: []string{"OIDC_ISSUER_URL must be an absolute URL"},
		},
		{
			name:    "non positive and out of range",
			mutate:  func(m map[string]string) { m["SQS_CONSUMER_WORKERS"] = "0"; m["SQS_WAIT_TIME"] = "30s" },
			wantErr: []string{"SQS_CONSUMER_WORKERS must be positive", "SQS_WAIT_TIME"},
		},
		{
			name:    "backoff bounds",
			mutate:  func(m map[string]string) { m["REFERENCE_BASE_BACKOFF"] = "10m"; m["REFERENCE_MAX_BACKOFF"] = "1m" },
			wantErr: []string{"REFERENCE_MAX_BACKOFF must be >= REFERENCE_BASE_BACKOFF"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := validEnv()
			if tt.mutate != nil {
				tt.mutate(m)
			}
			cfg, err := LoadFrom(env(m))
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatal("expected error")
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not mention %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestLoadFromConcurrent(t *testing.T) {
	t.Parallel()
	m := validEnv()
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if _, err := LoadFrom(env(m)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
