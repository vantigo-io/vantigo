-- +goose Up
-- A general Idempotency-Key (idempotency design I5, I11, I20, I21, I26, I27,
-- I28, docs/superpowers/specs/2026-11-19-idempotency-key-design.md).

-- One row per key a principal sent on a creating POST the router covers. The
-- scope is (principal_id, via, operation_id, key): principal_id is the
-- integration for an API key and the person (or agent) otherwise; via is the
-- way in for a person — the session, a station, the Assistant's or an agent's
-- grant, with the customer the grant is scoped to — so a key sent from one is
-- never answered to another (I28). fingerprint is an HMAC, under a key derived
-- from APP_SECRET, of the request's digest (I24). state is in_progress while
-- the claimer's lease_id holds it, done with the stored answer, or unknown
-- when the first request's outcome cannot be told (I23). answer is the stored
-- answer — its status, the headers a replay carries and its body, as one
-- plaintext — sealed with the installation's secret and bound to the row's
-- scope (I20): a body of at most 256 KB, headers of at most 8 KB, plus the
-- encoding's and the seal's few bytes. customer_id is the customer an agent's
-- run was scoped to, so anonymising that customer deletes the run's keys.
-- There is no FK: the principal may be deleted, and every row expires 24
-- hours after its claim, when the identity worker prunes it.
CREATE TABLE identity.idempotency_keys (
    principal_id uuid         NOT NULL,
    via          varchar(200) NOT NULL,
    operation_id varchar(200) NOT NULL,
    key          varchar(255) NOT NULL,
    fingerprint  bytea        NOT NULL CHECK (octet_length(fingerprint) = 32),
    state        text         NOT NULL CHECK (state IN ('in_progress', 'done', 'unknown')),
    lease_id     uuid         NOT NULL,
    lease_until  timestamptz  NOT NULL,
    answer       bytea        CHECK (octet_length(answer) <= 262144 + 8192 + 64),
    customer_id  integer,
    created_at   timestamptz  NOT NULL,
    expires_at   timestamptz  NOT NULL,
    PRIMARY KEY (principal_id, via, operation_id, key),
    CHECK (answer IS NULL OR state = 'done')
);
-- The prune deletes by expiry.
CREATE INDEX ix_idempotency_keys_expires_at ON identity.idempotency_keys (expires_at);
-- A customer's anonymisation deletes its agent runs' keys.
CREATE INDEX ix_idempotency_keys_customer ON identity.idempotency_keys (customer_id) WHERE customer_id IS NOT NULL;

-- The erasure watermark (I20): when a customer was last anonymised. A stored
-- answer claimed before it may name that customer, wherever it came from, so
-- it is never replayed: the key reads as unknown, and its row is pruned when
-- it expires, within 24 hours. One row at most.
CREATE TABLE identity.idempotency_erasure (
    singleton     boolean     PRIMARY KEY DEFAULT true CHECK (singleton),
    erased_before timestamptz NOT NULL
);

-- The request log's reason is a refusal's or a replay's (I22).
COMMENT ON COLUMN identity.api_requests.reason IS
    'Why the request was refused (revoked, disabled, archived, expired, network, never, read_only, forbidden, rate_limited), or idempotent_replay for an answer replayed for an Idempotency-Key.';

-- +goose Down
COMMENT ON COLUMN identity.api_requests.reason IS NULL;
DROP TABLE identity.idempotency_erasure;
DROP TABLE identity.idempotency_keys;
