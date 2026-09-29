-- 0008_outbox_status_attempts: publish attempts, last error, and parking of messages
-- the broker can never accept (ADR-0004 amendment 1, spec outbox-attempt-tracking).

ALTER TABLE outbox
    ADD COLUMN status          text        NOT NULL DEFAULT 'PENDING'
        CONSTRAINT ck_outbox_status CHECK (status IN ('PENDING', 'PUBLISHED', 'FAILED')), -- FAILED = parked
    ADD COLUMN attempts        int         NOT NULL DEFAULT 0, -- failed publish attempts
    ADD COLUMN last_error      text,                           -- broker error text, never payload
    ADD COLUMN last_attempt_at timestamptz;

UPDATE outbox SET status = 'PUBLISHED' WHERE published_at IS NOT NULL;

-- The relay only ever scans PENDING rows, oldest first.
DROP INDEX IF EXISTS ix_outbox_unpublished;
CREATE INDEX ix_outbox_pending ON outbox (created_at, id) WHERE status = 'PENDING';
