CREATE TABLE IF NOT EXISTS wager_transactions(
  id UUID NOT NULL PRIMARY KEY,
  origin  TEXT NOT NULL CHECK (origin IN ('INTERNAL', 'EXTERNAL')),
  provider_id TEXT NOT NULL,
  external_transaction_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL UNIQUE,
  payload_hash TEXT NOT NULL,
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  player_id UUID NOT NULL,
  round_id TEXT,
  game_id TEXT,
  kind TEXT NOT NULL CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
  status TEXT NOT NULL CHECK (status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')),
  amount BIGINT NOT NULL CHECK (amount >= 0),
  currency CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  reference_external_transaction_id TEXT,
  reference_transaction_id UUID REFERENCES wager_transactions (id),
  failure_code TEXT,
  observed_balance_amount BIGINT CHECK (observed_balance_amount >= 0),
  observed_wallet_version BIGINT CHECK (observed_wallet_version >= 1),
  attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CONSTRAINT uq_provider_external_tx UNIQUE (provider_id, external_transaction_id),
  UNIQUE (provider_id, idempotency_key)
);

CREATE UNIQUE INDEX wager_transactions_provider_key_unique
    ON wager_transactions (provider_id, idempotency_key);

CREATE UNIQUE INDEX wager_transactions_provider_external_unique
    ON wager_transactions (provider_id, external_transaction_id);
