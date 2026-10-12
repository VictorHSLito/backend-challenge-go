CREATE TABLE IF NOT EXISTS outbox(
  event_id UUID NOT NULL PRIMARY KEY,
  aggregate_id UUID NOT NULL,
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_delivery_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at TIMESTAMPTZ
);

CREATE INDEX idx_outbox_pending
  ON outbox (next_delivery_at, event_id)
  WHERE published_at IS NULL;
