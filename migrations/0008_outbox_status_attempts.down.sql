-- Reverts 0008. FAILED rows become unpublished again (the relay will retry them).
DROP INDEX IF EXISTS ix_outbox_pending;
ALTER TABLE IF EXISTS outbox
    DROP COLUMN IF EXISTS last_attempt_at,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS status;
DO $$
BEGIN
    IF to_regclass('public.outbox') IS NOT NULL THEN
        CREATE INDEX IF NOT EXISTS ix_outbox_unpublished ON outbox (created_at, id) WHERE published_at IS NULL;
    END IF;
END $$;
