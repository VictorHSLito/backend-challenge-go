CREATE TABLE IF NOT EXISTS wallet_ledger_entries(
  id UUID NOT NULL PRIMARY KEY,
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  transaction_id UUID NOT NULL REFERENCES wager_transactions (id),
  direction TEXT NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
  currency CHAR(3) NOT NULL,
  amount BIGINT NOT NULL CHECK (amount > 0),
  balance_before BIGINT NOT NULL CHECK (balance_before >= 0),
  balance_after BIGINT NOT NULL CHECK (balance_after >= 0),
  created_at TIMESTAMPZ NOT NULL,
  CONSTRAINT uq_wallet_transaction UNIQUE (wallet_id, transaction_id)
);

CREATE INDEX wallet_ledger_entries_cursor 
  ON wallet_ledger_entries (wallet_id, created_at, id);
