-- 0001_init: accounts, transfers, audit_log (spec intra-bank-transfer §4)
-- Naming is golang-migrate compatible: NNNN_name.{up,down}.sql

CREATE TABLE accounts (
    id          uuid        PRIMARY KEY,
    customer_id text        NOT NULL,
    currency    char(3)     NOT NULL,
    balance     bigint      NOT NULL CONSTRAINT ck_accounts_balance_non_negative CHECK (balance >= 0), -- minor units
    status      text        NOT NULL CONSTRAINT ck_accounts_status CHECK (status IN ('ACTIVE', 'FROZEN', 'CLOSED')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_accounts_customer_id ON accounts (customer_id);

CREATE TABLE transfers (
    id              uuid        PRIMARY KEY,
    requested_by    text        NOT NULL,
    idempotency_key text        NOT NULL,
    request_hash    char(64)    NOT NULL,
    from_account_id uuid        NOT NULL REFERENCES accounts (id),
    to_account_id   uuid        NOT NULL REFERENCES accounts (id),
    amount          bigint      NOT NULL CONSTRAINT ck_transfers_amount_positive CHECK (amount > 0),
    currency        char(3)     NOT NULL,
    reference       text        NOT NULL DEFAULT '' CONSTRAINT ck_transfers_reference_len CHECK (char_length(reference) <= 140),
    status          text        NOT NULL,
    created_at      timestamptz NOT NULL,
    CONSTRAINT uq_transfers_idempotency UNIQUE (requested_by, idempotency_key),
    CONSTRAINT ck_transfers_distinct_accounts CHECK (from_account_id <> to_account_id)
);
CREATE INDEX idx_transfers_from_account ON transfers (from_account_id, created_at);
CREATE INDEX idx_transfers_to_account ON transfers (to_account_id, created_at);

CREATE TABLE audit_log (
    id            uuid        PRIMARY KEY,
    actor         text        NOT NULL,
    action        text        NOT NULL,
    resource_type text        NOT NULL,
    resource_id   text        NOT NULL,
    trace_id      text        NOT NULL,
    before        jsonb,
    after         jsonb,
    occurred_at   timestamptz NOT NULL
);
CREATE INDEX idx_audit_log_resource ON audit_log (resource_type, resource_id);

-- Audit log is append-only, even for the application role (spec AC-11).
CREATE FUNCTION audit_log_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only';
END;
$$;
CREATE TRIGGER trg_audit_log_immutable
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
