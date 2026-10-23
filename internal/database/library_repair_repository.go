package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"time"
)

// LibraryRepairStatus values for the repair ledger.
const (
	LibraryRepairPending            = "pending"
	LibraryRepairLeased             = "leased"
	LibraryRepairWaitingRetry       = "waiting_retry"
	LibraryRepairReviewRequired     = "review_required"
	LibraryRepairResolvedIdentity   = "resolved_identity"
	LibraryRepairPosterAdded        = "poster_added"
	LibraryRepairTerminalNoArtwork  = "terminal_no_artwork"
	LibraryRepairTerminalUnresolved = "terminal_unresolvable"
)

// LibraryRepairEntry is one row of the repair ledger: restart-safe,
// fair, auditable bookkeeping for poster/identity repair.
type LibraryRepairEntry struct {
	MediaType        string `json:"media_type"`
	IMDbID           string `json:"imdb_id"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	Attempts         int    `json:"attempts"`
	NextAttempt      int64  `json:"next_attempt"`
	LeaseExpires     int64  `json:"lease_expires"`
	LeaseToken       string `json:"lease_token"`
	InputFingerprint string `json:"input_fingerprint"`
	ResolverVersion  int    `json:"resolver_version"`
	Evidence         string `json:"evidence"`
	OriginalIMDbID   string `json:"original_imdb_id"`
	UpdatedAt        int64  `json:"updated_at"`
}

// isRepairLedgerMissing reports a pre-044 database so repair code degrades
// gracefully instead of failing imports on old schemas.
func isRepairLedgerMissing(err error) bool {
	return isLibrarySchemaMissing(err)
}

// EnsureLibraryRepairEntry creates a pending ledger row if none exists.
// Refreshes fingerprint/version/evidence on any unleased row whose input
// moved on (pending, waiting_retry, review, terminal): resets the attempt
// budget and re-queues immediately. Never touches rows holding a live lease.
// This is the convergence path: post-claim evidence changes release into
// pending, then the next Ensure refreshes the fingerprint so the next claim
// works with fresh evidence instead of cycling on stale input.
func (r *Repository) EnsureLibraryRepairEntry(ctx context.Context, mediaType, imdbID, fingerprint, evidence string) error {
	return r.EnsureLibraryRepairEntryVersion(ctx, mediaType, imdbID, fingerprint, evidence, libraryRepairLedgerVersion())
}

// libraryRepairLedgerVersion is the resolver version written for new rows.
// Kept in database package to avoid an api import cycle.
func libraryRepairLedgerVersion() int { return 2 }

func (r *Repository) EnsureLibraryRepairEntryVersion(ctx context.Context, mediaType, imdbID, fingerprint, evidence string, resolverVersion int) error {
	now := time.Now().Unix()
	_, err := r.db.ExecContext(ctx, `INSERT INTO library_repair_ledger(media_type,imdb_id,status,reason,attempts,next_attempt,lease_expires,input_fingerprint,resolver_version,evidence,original_imdb_id,updated_at) VALUES(?,?,?,?,?,?,?, ?,?,?,?,?) ON CONFLICT(media_type,imdb_id) DO NOTHING`,
		mediaType, imdbID, LibraryRepairPending, "", 0, 0, 0, fingerprint, resolverVersion, evidence, imdbID, now)
	if err != nil {
		if isRepairLedgerMissing(err) {
			return nil
		}
		return err
	}
	// Refresh any unleased row whose evidence or resolver moved on: reset
	// budget, re-queue now. Covers pending/waiting (fresh search input),
	// review/terminal (parked recovery). Live leases untouched.
	_, err = r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET status=?, reason='evidence_changed', attempts=0, next_attempt=?, input_fingerprint=?, resolver_version=?, evidence=?, updated_at=? WHERE media_type=? AND imdb_id=? AND status IN (?,?,?,?,?) AND (input_fingerprint<>? OR resolver_version<>?) AND lease_expires<=?`,
		LibraryRepairPending, now, fingerprint, resolverVersion, evidence, now, mediaType, imdbID,
		LibraryRepairPending, LibraryRepairWaitingRetry, LibraryRepairReviewRequired, LibraryRepairTerminalUnresolved, LibraryRepairTerminalNoArtwork,
		fingerprint, resolverVersion, now)
	if err != nil && isRepairLedgerMissing(err) {
		return nil
	}
	return err
}

// LeaseLibraryRepairDue atomically leases up to limit due rows (pending or
// waiting_retry with next_attempt elapsed, or expired leases), oldest-first.
// Canonical and synthetic quotas are applied BEFORE claiming so synthetic
// history cannot starve canonical retries: half the limit is reserved for
// canonical tt titles. Returns the leased entries with fresh lease expiry.
func (r *Repository) LeaseLibraryRepairDue(ctx context.Context, limit int, leaseSeconds int64) ([]LibraryRepairEntry, error) {
	return r.LeaseLibraryRepairDueQuotas(ctx, limit/2, limit-limit/2, leaseSeconds)
}

// LeaseLibraryRepairDueQuotas leases up to canonicalLimit tt rows and
// syntheticLimit altm rows, oldest-first each. Expired leases recover first
// (crash-safe). Each claim mints a unique lease_token; ALL result writes
// must match (media_type, imdb_id, lease_token) — ownership-fenced, so an
// expired worker can never overwrite a newer outcome. Claims exclude rows
// holding a live lease (lease_expires in the future).
func (r *Repository) LeaseLibraryRepairDueQuotas(ctx context.Context, canonicalLimit, syntheticLimit int, leaseSeconds int64) ([]LibraryRepairEntry, error) {
	now := time.Now().Unix()
	leaseExpires := now + leaseSeconds
	// Expired leases become due again first (crash recovery).
	_, err := r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET status=?, lease_expires=0 WHERE status=? AND lease_expires>0 AND lease_expires<?`,
		LibraryRepairPending, LibraryRepairLeased, now)
	if err != nil {
		if isRepairLedgerMissing(err) {
			return nil, nil
		}
		return nil, err
	}
	lease := func(canonical bool, quota int) ([]LibraryRepairEntry, error) {
		if quota <= 0 {
			return nil, nil
		}
		pattern := "tt%"
		op := "LIKE"
		if !canonical {
			op = "NOT LIKE"
		}
		// Eligible: pending/waiting_retry due, or leased-but-expired — and
		// never holding a LIVE lease (excludes future lease_expires).
		rows, err := r.db.QueryContext(ctx, `SELECT media_type,imdb_id,status,reason,attempts,next_attempt,lease_expires,lease_token,input_fingerprint,resolver_version,evidence,original_imdb_id,updated_at FROM library_repair_ledger WHERE (status=? OR status=? OR (status=? AND next_attempt<=?)) AND (lease_expires=0 OR lease_expires<=?) AND imdb_id `+op+` ? ORDER BY next_attempt,updated_at LIMIT ?`,
			LibraryRepairPending, LibraryRepairLeased, LibraryRepairWaitingRetry, now, now, pattern, quota)
		if err != nil {
			if isRepairLedgerMissing(err) {
				return nil, nil
			}
			return nil, err
		}
		defer rows.Close()
		var out []LibraryRepairEntry
		for rows.Next() {
			var e LibraryRepairEntry
			if err := rows.Scan(&e.MediaType, &e.IMDbID, &e.Status, &e.Reason, &e.Attempts, &e.NextAttempt, &e.LeaseExpires, &e.LeaseToken, &e.InputFingerprint, &e.ResolverVersion, &e.Evidence, &e.OriginalIMDbID, &e.UpdatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		leased := out[:0]
		for _, e := range out {
			token := newRepairLeaseToken()
			// Ownership-fenced claim: row must still be unleased-or-expired
			// AND still in a claimable status. Winners get a fresh token.
			res, err := r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET status=?, lease_expires=?, lease_token=?, updated_at=? WHERE media_type=? AND imdb_id=? AND (status=? OR status=? OR (status=? AND next_attempt<=?)) AND (lease_expires=0 OR lease_expires<=?)`,
				LibraryRepairLeased, leaseExpires, token, now, e.MediaType, e.IMDbID, LibraryRepairPending, LibraryRepairLeased, LibraryRepairWaitingRetry, now, now)
			if err != nil {
				continue
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue
			}
			e.Status = LibraryRepairLeased
			e.LeaseExpires = leaseExpires
			e.LeaseToken = token
			leased = append(leased, e)
		}
		return leased, nil
	}
	canonical, err := lease(true, canonicalLimit)
	if err != nil {
		return nil, err
	}
	synthetic, err := lease(false, syntheticLimit)
	if err != nil {
		return canonical, err
	}
	return append(canonical, synthetic...), nil
}

// UpdateLibraryRepairResult records one attempt outcome. Terminal states stop
// retries; waiting_retry schedules backoff; review_required parks for manual
// identification without burning provider calls. Writes are ownership-fenced
// by lease token: ONLY the holding worker's token may write. An expired
// worker, a reclaimed worker, or classification can never overwrite a newer
// outcome. Classification (no attempt taken) uses RecordRepairNoAttempt,
// which additionally refuses active leases.
func (r *Repository) UpdateLibraryRepairResult(ctx context.Context, mediaType, imdbID, status, reason string, nextAttempt int64, fingerprint, evidence string) error {
	return r.updateLibraryRepairResult(ctx, mediaType, imdbID, status, reason, nextAttempt, fingerprint, evidence, true)
}

// UpdateLibraryRepairResultToken is the ownership-fenced write: token must
// match the row's current lease_token.
func (r *Repository) UpdateLibraryRepairResultToken(ctx context.Context, mediaType, imdbID, token, status, reason string, nextAttempt int64, fingerprint, evidence string, countAttempt bool) error {
	now := time.Now().Unix()
	inc := "attempts"
	if countAttempt {
		inc = "attempts+1"
	}
	res, err := r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET status=?, reason=?, attempts=`+inc+`, next_attempt=?, lease_expires=0, lease_token='', input_fingerprint=?, evidence=?, updated_at=? WHERE media_type=? AND imdb_id=? AND lease_token=? AND lease_token<>''`,
		status, reason, nextAttempt, fingerprint, evidence, now, mediaType, imdbID, token)
	if err != nil {
		if isRepairLedgerMissing(err) {
			return nil
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return nil
}

// RecordLibraryRepairNoAttempt parks or re-keys a row WITHOUT consuming the
// attempt budget (classification is observation, not a provider attempt).
// Refuses rows holding a live lease owned by someone else.
func (r *Repository) RecordLibraryRepairNoAttempt(ctx context.Context, mediaType, imdbID, status, reason string, nextAttempt int64, fingerprint, evidence string) error {
	return r.updateLibraryRepairResult(ctx, mediaType, imdbID, status, reason, nextAttempt, fingerprint, evidence, false)
}

func (r *Repository) updateLibraryRepairResult(ctx context.Context, mediaType, imdbID, status, reason string, nextAttempt int64, fingerprint, evidence string, countAttempt bool) error {
	now := time.Now().Unix()
	inc := "attempts"
	if countAttempt {
		inc = "attempts+1"
	}
	// Unleased legacy path (tests, manual callers): never touch a row
	// holding a LIVE lease — classification must not clear a worker.
	// Superseding an expired claim invalidates its token so the late worker
	// can never overwrite this decision (token writer requires equality).
	res, err := r.db.ExecContext(ctx, `UPDATE library_repair_ledger SET status=?, reason=?, attempts=`+inc+`, next_attempt=?, lease_expires=0, lease_token='', input_fingerprint=?, evidence=?, updated_at=? WHERE media_type=? AND imdb_id=? AND lease_expires<=?`,
		status, reason, nextAttempt, fingerprint, evidence, now, mediaType, imdbID, now)
	if err != nil {
		if isRepairLedgerMissing(err) {
			return nil
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return nil
}

// MarkLibraryRepairDone records a successful outcome (resolved_identity,
// poster_added) and frees the lease.
func (r *Repository) MarkLibraryRepairDone(ctx context.Context, mediaType, imdbID, status, reason string) error {
	return r.UpdateLibraryRepairResult(ctx, mediaType, imdbID, status, reason, 0, "", "")
}

// newRepairLeaseToken mints a unique claim token per lease (ownership fence).
func newRepairLeaseToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	now := time.Now().UnixNano()
	for i := 0; i < 8; i++ {
		b[i] ^= byte(now >> (i * 8))
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 32)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0xf]
	}
	return string(out)
}

type LibraryRepairStats struct {
	Pending        int `json:"pending"`
	WaitingRetry   int `json:"waiting_retry"`
	ReviewRequired int `json:"review_required"`
	Resolved       int `json:"resolved_identity"`
	PosterAdded    int `json:"poster_added"`
	Terminal       int `json:"terminal"`
}

func (r *Repository) LibraryRepairStats(ctx context.Context) (LibraryRepairStats, error) {
	var s LibraryRepairStats
	rows, err := r.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM library_repair_ledger GROUP BY status`)
	if err != nil {
		if isRepairLedgerMissing(err) {
			return s, nil
		}
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			continue
		}
		switch status {
		case LibraryRepairPending, LibraryRepairLeased:
			s.Pending += n
		case LibraryRepairWaitingRetry:
			s.WaitingRetry += n
		case LibraryRepairReviewRequired:
			s.ReviewRequired += n
		case LibraryRepairResolvedIdentity:
			s.Resolved += n
		case LibraryRepairPosterAdded:
			s.PosterAdded += n
		case LibraryRepairTerminalNoArtwork, LibraryRepairTerminalUnresolved:
			s.Terminal += n
		}
	}
	return s, rows.Err()
}

// LibraryRepairReviewQueue lists titles parked for manual identification.
func (r *Repository) LibraryRepairReviewQueue(ctx context.Context, limit int) ([]LibraryRepairEntry, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT media_type,imdb_id,status,reason,attempts,next_attempt,lease_expires,lease_token,input_fingerprint,resolver_version,evidence,original_imdb_id,updated_at FROM library_repair_ledger WHERE status=? ORDER BY updated_at DESC LIMIT ?`,
		LibraryRepairReviewRequired, limit)
	if err != nil {
		if isRepairLedgerMissing(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []LibraryRepairEntry
	for rows.Next() {
		var e LibraryRepairEntry
		if err := rows.Scan(&e.MediaType, &e.IMDbID, &e.Status, &e.Reason, &e.Attempts, &e.NextAttempt, &e.LeaseExpires, &e.LeaseToken, &e.InputFingerprint, &e.ResolverVersion, &e.Evidence, &e.OriginalIMDbID, &e.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

var _ = json.RawMessage{}
var _ = sql.ErrNoRows
