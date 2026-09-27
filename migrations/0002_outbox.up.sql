-- 0002_outbox: transactional outbox for domain events (ADR-0004, spec payment-events-outbox)

CREATE TABLE outbox (
    id           uuid        PRIMARY KEY,           -- event id; consumers deduplicate on it
    topic        text        NOT NULL,
    message_key  text        NOT NULL,              -- Kafka partition key (aggregate id)
    payload      bytea       NOT NULL,              -- exact bytes published, see contracts/asyncapi.yaml
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz                        -- NULL until the relay has published it
);

-- The relay only ever scans unpublished rows, oldest first.
CREATE INDEX ix_outbox_unpublished ON outbox (created_at, id) WHERE published_at IS NULL;
