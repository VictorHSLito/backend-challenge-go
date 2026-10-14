ALTER TABLE wager_transactions
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS wager_transactions_pending_reference_scan
    ON wager_transactions (next_attempt_at NULLS FIRST, created_at)
    WHERE status = 'PENDING_REFERENCE';
