CREATE TABLE IF NOT EXISTS wager_transactions(
  id UUID NOT NULL PRIMARY KEY,
  provider_id TEXT NOT NULL,
  external_transaction_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL UNIQUE,
  payload_hash TEXT NOT NULL,
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  player_id UUID NOT NULL,
  round_id TEXT,
  game_id TEXT,
  kind TEXT NOT NULL CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
  status TEXT NOT NULL,
  amount BIGINT NOT NULL CHECK (amount >= 0),
  currency CHAR(3) NOT NULL,
  reference_external_transaction_id TEXT,
  failure_code TEXT,
  observed_balance_amount BIGINT,
  observed_wallet_version BIGINT,
  created_at TIMESTAMPZ NOT NULL,
  updated_at TIMESTAMPZ NOT NULL,
  CONSTRAINT uq_provider_external_tx UNIQUE (provider_id, external_transaction_id),
  CONSTRAINT UNIQUE (provider_id, idempotency_key)
);

CREATE UNIQUE INDEX wager_transactions_provider_key_unique
    ON wager_transactions (provider_id, idempotency_key);

CREATE UNIQUE INDEX wager_transactions_provider_external_unique
    ON wager_transactions (provider_id, external_transaction_id);
