CREATE TABLE IF NOT EXISTS wallets(
  id UUID PRIMARY KEY,
  player_id UUID NOT NULL,
  currency CHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  balance_amount BIGINT NOT NULL CHECK (balance_amount >=0),
  version BIGINT NOT NULL CHECK (version >= 1),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CONSTRAINT uq_player_currency UNIQUE(player_id, currency)
);
