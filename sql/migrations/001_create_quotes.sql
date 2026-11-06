-- +goose Up
CREATE TABLE quotes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    pair TEXT NOT NULL,
    idempotency_key TEXT UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending',
    price DOUBLE PRECISION,
    updated_at TIMESTAMPTZ,
    claim_version BIGINT NOT NULL DEFAULT 0,
    lease_until TIMESTAMPTZ,
    CHECK (
        (status = 'completed' AND price IS NOT NULL AND updated_at IS NOT NULL)
        OR (status IN ('pending', 'failed') AND price IS NULL AND updated_at IS NULL)
    )
);

CREATE INDEX quotes_pending_idx ON quotes (id) WHERE status = 'pending';
CREATE INDEX quotes_latest_idx ON quotes (pair, updated_at DESC, id DESC) WHERE status = 'completed';

-- +goose Down
DROP TABLE quotes;
