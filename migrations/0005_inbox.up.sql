CREATE TABLE IF NOT EXISTS inbox (
  consumer_name TEXT NOT NULL,
  message_id TEXT NOT NULL,
  message_hash TEXT NOT NULL,
  received_at TIMESTAMPZ NOT NULL,
  completed_at TIMESTAMPZ NOT NULL,
  PRIMARY KEY (consumer_id, message_id)
);
