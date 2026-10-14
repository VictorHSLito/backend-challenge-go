//go:build integration

// Package integration holds end-to-end tests that boot a real Postgres via
// testcontainers and exercise the service/repository stack. These tests
// are guarded by the `integration` build tag so `go test ./...` stays
// fast and dependency-free; run them with:
//
//	go test -tags=integration -race ./test/integration/...
package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/victorhslito/junglegaming/internal/repository/postgres"
	"github.com/victorhslito/junglegaming/internal/service"
	"github.com/victorhslito/junglegaming/internal/testinfra"
)

// pg is the shared container for every test in this package. One
// container, truncated between tests — ~15× faster than booting fresh.
var pg *testinfra.PostgresContainer

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	pg, err = testinfra.StartPostgres(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start postgres container: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	pg.Close(ctx)
	os.Exit(code)
}

// services wires a fresh service bundle on the shared pool. We truncate
// first so each test's state is isolated from the previous one.
type services struct {
	wallet *service.WalletService
	tx     *service.TransactionService
}

func newServices(t *testing.T) services {
	t.Helper()
	pg.Truncate(context.Background(), t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tr := postgres.NewTxRunner(pg.Pool)
	walletRepo := postgres.NewWalletRepo(pg.Pool)
	txRepo := postgres.NewTransactionRepo(pg.Pool)
	outboxRepo := postgres.NewOutboxRepo(pg.Pool)
	inboxRepo := postgres.NewInboxRepo(pg.Pool)
	return services{
		wallet: service.NewWalletService(tr, walletRepo, txRepo, outboxRepo, logger),
		tx:     service.NewTransactionService(tr, walletRepo, txRepo, outboxRepo, inboxRepo, logger),
	}
}
