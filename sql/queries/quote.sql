-- name: EnqueueQuote :one
INSERT INTO quotes (pair, idempotency_key)
VALUES ($1, sqlc.narg(idempotency_key))
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING id;

-- name: GetQuoteByKey :one
SELECT id, pair FROM quotes WHERE idempotency_key = $1;

-- name: GetQuote :one
SELECT * FROM quotes WHERE id = $1;

-- name: GetLatestQuote :one
SELECT * FROM quotes
WHERE pair = $1 AND status = 'completed'
ORDER BY updated_at DESC, id DESC
LIMIT 1;

-- name: ClaimNextQuote :one
WITH next AS (
    SELECT id FROM quotes
    WHERE status = 'pending'
      AND (lease_until IS NULL OR lease_until <= clock_timestamp())
    ORDER BY id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE quotes AS q
SET claim_version = q.claim_version + 1,
    lease_until = clock_timestamp() + sqlc.arg(lease_microseconds)::bigint * interval '1 microsecond'
FROM next
WHERE q.id = next.id
RETURNING q.id, q.pair, q.claim_version;

-- name: CompleteQuote :execrows
UPDATE quotes
SET status = 'completed', price = $2, updated_at = clock_timestamp(), lease_until = NULL
WHERE id = $1 AND claim_version = $3 AND status = 'pending'
  AND lease_until > clock_timestamp();

-- name: FailQuote :execrows
UPDATE quotes SET status = 'failed', lease_until = NULL
WHERE id = $1 AND claim_version = $2 AND status = 'pending'
  AND lease_until > clock_timestamp();
