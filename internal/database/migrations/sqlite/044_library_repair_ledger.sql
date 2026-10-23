-- +goose Up
CREATE TABLE library_repair_ledger (
    media_type TEXT NOT NULL,
    imdb_id TEXT NOT NULL,
    -- pending | leased | waiting_retry | review_required | resolved_identity | poster_added | terminal_no_artwork | terminal_unresolvable
    status TEXT NOT NULL DEFAULT 'pending',
    -- machine reason code: empty_query | search_miss | search_ambiguous | enrich_transient | enrich_not_found | enrich_mismatch | relinked | poster_refreshed | needs_review | ...
    reason TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt BIGINT NOT NULL DEFAULT 0,
    lease_expires BIGINT NOT NULL DEFAULT 0,
    -- unique claim token per lease; result writes must match it.
    lease_token TEXT NOT NULL DEFAULT '',
    -- sha256 of the identity input (name+type+year+parent evidence) so
    -- re-runs skip unchanged garbage until evidence changes.
    input_fingerprint TEXT NOT NULL DEFAULT '',
    resolver_version INTEGER NOT NULL DEFAULT 1,
    -- compact identity evidence (parent folder, nzb name, year) + original
    -- identity for audit/rollback. Never credentials.
    evidence TEXT NOT NULL DEFAULT '{}',
    original_imdb_id TEXT NOT NULL DEFAULT '',
    updated_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (media_type, imdb_id)
);
CREATE INDEX IF NOT EXISTS library_repair_ledger_due ON library_repair_ledger(status, next_attempt);
CREATE INDEX IF NOT EXISTS library_repair_ledger_review ON library_repair_ledger(status) WHERE status = 'review_required';

-- +goose Down
DROP INDEX IF EXISTS library_repair_ledger_review;
DROP INDEX IF EXISTS library_repair_ledger_due;
DROP TABLE library_repair_ledger;
