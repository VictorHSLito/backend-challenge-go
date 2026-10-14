DROP INDEX IF EXISTS wager_transactions_pending_reference_scan;

ALTER TABLE wager_transactions
    DROP COLUMN IF EXISTS next_attempt_at;
