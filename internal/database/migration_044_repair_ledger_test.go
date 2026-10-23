package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func setupRepairLedgerDB(t *testing.T) *Repository {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, f := range []string{"migrations/sqlite/042_saved_library.sql", "migrations/sqlite/044_library_repair_ledger.sql"} {
		migration, err := os.ReadFile(f)
		require.NoError(t, err)
		_, err = db.Exec(strings.Split(string(migration), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	return NewRepository(db, DialectSQLite)
}

func TestMigration044_RepairLedger(t *testing.T) {
	r := setupRepairLedgerDB(t)
	ctx := context.Background()
	// Ledger table exists after migrations (lease_token defaults '').
	_, err := r.db.ExecContext(ctx, `INSERT INTO library_repair_ledger(media_type,imdb_id,status,reason,attempts,next_attempt,lease_expires,lease_token,input_fingerprint,resolver_version,evidence,original_imdb_id,updated_at) VALUES('movie','tt0133093','pending','',0,0,0,'','fp1',2,'{}','tt0133093',0)`)
	require.NoError(t, err)
	// Ensure is idempotent on identical input: row untouched.
	require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "movie", "tt0133093", "fp1", "{}"))
	// Ensure refreshes unleased rows on evidence change (resets budget), but
	// never touches in-flight leases.
	_, err = r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET status=?, lease_expires=9999999999, lease_token='live-token' WHERE media_type='movie' AND imdb_id='tt0133093'`, LibraryRepairLeased)
	require.NoError(t, err)
	require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "movie", "tt0133093", "fp2", "{}"))
	var status, fp, token string
	var attempts int
	require.NoError(t, r.db.QueryRowContext(ctx, `SELECT status,input_fingerprint,attempts,lease_token FROM library_repair_ledger WHERE media_type='movie' AND imdb_id='tt0133093'`).Scan(&status, &fp, &attempts, &token))
	require.Equal(t, "leased", status)
	require.Equal(t, "fp1", fp, "ensure must not touch in-flight leases")
	require.Equal(t, "live-token", token)
	// Release the live lease, then evidence change refreshes the row.
	_, err = r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET lease_expires=0, status=? WHERE media_type='movie' AND imdb_id='tt0133093'`, LibraryRepairPending)
	require.NoError(t, err)
	require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "movie", "tt0133093", "fp2", "{}"))
	require.NoError(t, r.db.QueryRowContext(ctx, `SELECT status,input_fingerprint,attempts FROM library_repair_ledger WHERE media_type='movie' AND imdb_id='tt0133093'`).Scan(&status, &fp, &attempts))
	require.Equal(t, "pending", status)
	require.Equal(t, "fp2", fp, "ensure refreshes unleased evidence")
	require.Equal(t, 0, attempts)

	// Lease due rows oldest-first; claims mint unique tokens.
	// (tt0133093 is pending+unleased after the evidence refresh above.)
	require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "series", "tt0903747", "fp3", "{}"))
	leased, err := r.LeaseLibraryRepairDueQuotas(ctx, 1, 0, 300)
	require.NoError(t, err)
	require.Len(t, leased, 1)
	require.Equal(t, "tt0903747", leased[0].IMDbID)
	require.Equal(t, LibraryRepairLeased, leased[0].Status)
	require.NotEmpty(t, leased[0].LeaseToken)
	holderToken := leased[0].LeaseToken

	// Expired worker A cannot overwrite reclaimed worker B: stale token
	// no-ops, state unchanged.
	_, err = r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET status=?, lease_expires=?, lease_token='token-B' WHERE media_type='series' AND imdb_id='tt0903747'`, LibraryRepairLeased, 9999999999)
	require.NoError(t, err)
	require.NoError(t, r.UpdateLibraryRepairResultToken(ctx, "series", "tt0903747", holderToken, LibraryRepairWaitingRetry, "enrich_transient", 9999999999, "fp3", "{}", true))
	var afterStatus, afterToken string
	var afterAttempts int
	require.NoError(t, r.db.QueryRowContext(ctx, `SELECT status,lease_token,attempts FROM library_repair_ledger WHERE media_type='series' AND imdb_id='tt0903747'`).Scan(&afterStatus, &afterToken, &afterAttempts))
	require.Equal(t, "leased", afterStatus, "stale token must not write")
	require.Equal(t, "token-B", afterToken)
	require.Equal(t, 0, afterAttempts, "stale token must not consume budget")
	// Holder B writes successfully and frees the lease.
	require.NoError(t, r.UpdateLibraryRepairResultToken(ctx, "series", "tt0903747", "token-B", LibraryRepairWaitingRetry, "enrich_transient", 9999999999, "fp3", "{}", true))
	require.NoError(t, r.db.QueryRowContext(ctx, `SELECT status,attempts FROM library_repair_ledger WHERE media_type='series' AND imdb_id='tt0903747'`).Scan(&afterStatus, &afterAttempts))
	require.Equal(t, "waiting_retry", afterStatus)
	require.Equal(t, 1, afterAttempts)

	// A writing after B completes is fenced (B freed the token).
	require.NoError(t, r.UpdateLibraryRepairResultToken(ctx, "series", "tt0903747", "token-B", LibraryRepairReviewRequired, "empty_query", 9999999999, "fp3", "{}", false))
	require.NoError(t, r.db.QueryRowContext(ctx, `SELECT status FROM library_repair_ledger WHERE media_type='series' AND imdb_id='tt0903747'`).Scan(&afterStatus))
	require.Equal(t, "waiting_retry", afterStatus, "post-completion stale write fenced")

	// Classification during an active lease is refused.
	_, err = r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET status=?, lease_expires=?, lease_token='live-B' WHERE media_type='series' AND imdb_id='tt0903747'`, LibraryRepairLeased, 9999999999)
	require.NoError(t, err)
	require.NoError(t, r.RecordLibraryRepairNoAttempt(ctx, "series", "tt0903747", LibraryRepairReviewRequired, "empty_query", 9999999999, "fp3", "{}"))
	require.NoError(t, r.db.QueryRowContext(ctx, `SELECT status FROM library_repair_ledger WHERE media_type='series' AND imdb_id='tt0903747'`).Scan(&afterStatus))
	require.Equal(t, "leased", afterStatus, "classification must not clear active lease")
	// Release the live lease for the remainder of the test.
	_, err = r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET status=?, lease_expires=0, lease_token='' WHERE media_type='series' AND imdb_id='tt0903747'`, LibraryRepairPending)
	require.NoError(t, err)

	// Quota fairness: canonical reserved even with synthetic backlog.
	// Request BOTH quotas at once: canonical inclusion + oldest-first.
	for i := 0; i < 5; i++ {
		require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "movie", "altm-movie-synth", "fp", "{}"))
		_ = i
	}
	require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "movie", "tt0999999", "fp", "{}"))
	require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "movie", "altm-movie-s1", "fp", "{}"))
	require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "movie", "altm-movie-s2", "fp", "{}"))
	both, err := r.LeaseLibraryRepairDueQuotas(ctx, 2, 2, 300)
	require.NoError(t, err)
	canonIDs := []string{}
	synthCount := 0
	for _, e := range both {
		if len(e.IMDbID) >= 2 && e.IMDbID[:2] == "tt" {
			canonIDs = append(canonIDs, e.IMDbID)
		} else {
			synthCount++
		}
	}
	require.Contains(t, canonIDs, "tt0999999", "canonical quota reserves tt capacity")
	require.GreaterOrEqual(t, synthCount, 1)
	require.LessOrEqual(t, len(both), 4)

	// Result recording + stats (fresh row to avoid interference above).
	// Note: tt0903747 sits in leased state from the fence block, so only
	// tt1111111 lands in review here.
	require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "series", "tt1111111", "fp9", "{}"))
	require.NoError(t, r.UpdateLibraryRepairResult(ctx, "series", "tt1111111", LibraryRepairReviewRequired, "empty_query", 9999999999, "fp9", "{}"))
	stats, err := r.LibraryRepairStats(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats.ReviewRequired)

	review, err := r.LibraryRepairReviewQueue(ctx, 10)
	require.NoError(t, err)
	require.Len(t, review, 1)
	require.Equal(t, "tt1111111", review[0].IMDbID)

	require.NoError(t, r.MarkLibraryRepairDone(ctx, "series", "tt1111111", LibraryRepairResolvedIdentity, "relinked"))
	stats, err = r.LibraryRepairStats(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Resolved)
	require.Equal(t, 0, stats.ReviewRequired)
}

func TestMigration044_RepairLedgerDown(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	r := NewRepository(db, DialectSQLite)
	ctx := context.Background()
	// Pre-044 databases degrade gracefully: no error, no rows.
	require.NoError(t, r.EnsureLibraryRepairEntry(ctx, "movie", "tt0000001", "fp", "{}"))
	leased, err := r.LeaseLibraryRepairDue(ctx, 10, 300)
	require.NoError(t, err)
	require.Empty(t, leased)
	stats, err := r.LibraryRepairStats(ctx)
	require.NoError(t, err)
	require.Equal(t, LibraryRepairStats{}, stats)
}
