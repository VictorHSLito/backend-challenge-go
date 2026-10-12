CREATE TABLE IF NOT EXISTS inbox (
  consumer_name TEXT NOT NULL,
  message_id TEXT NOT NULL,
  message_hash TEXT NOT NULL,
  transaction_id UUID REFERENCES wager_transactions (id),
  received_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ,
  PRIMARY KEY (consumer_name, message_id)
);
