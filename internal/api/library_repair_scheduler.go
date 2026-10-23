package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kipsilabs/altmount/internal/database"
)

// runLibraryRepairPass is the single scheduler replacing the competing
// refreshPosterless (200/run, unmarked retries) and refreshStale (25/run)
// loops. It processes bounded, fairly interleaved canonical (tt) and
// synthetic (altm-) queues from the durable ledger (044):
//   - atomic leases, expired-lease recovery (crash-safe)
//   - capped exponential backoff + jitter, Retry-After honored by callers
//   - garbage suppressed until evidence/resolver version changes
//   - review_required parks hash-only titles without burning providers
//
// Filtering semantics are untouched: stremio_only, backend allowlists, and
// LibraryStremioOnly order are preserved. No response-shape changes.
func (s *Server) runLibraryRepairPass(ctx context.Context, userAgent string) libraryRepairCounters {
	var c libraryRepairCounters
	if s.queueRepo == nil {
		return c
	}
	// Phase 0 (observation/classification): ensure every posterless title has
	// a ledger row keyed by current identity + evidence fingerprint.
	// Classification is observation only: it never consumes the attempt
	// budget and never overwrites in-flight leases.
	titles, err := s.queueRepo.LibraryTitles(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Library repair pass aborted: titles unavailable", "error", err)
		return c
	}
	// Phase -1 (identity audit): detach releases whose parsed release year
	// conflicts with their title's canonical year (a wrong search once
	// merged "Africa (2001)" and "Africa (2013)" onto one IMDb ID). Detached
	// releases land on a year-keyed synthetic altm-* anchor and re-enter the
	// normal repair pipeline, where the year-strict search re-resolves them.
	s.auditLibraryIdentitySplits(ctx, titles, &c)
	// The audit mutates title/release ownership; every later phase run on the
	// pre-split snapshot would miss the freshly created anchors and resolve
	// their ledger rows as "relinked_away" instead of repairing them.
	titles, err = s.queueRepo.LibraryTitles(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Library repair pass aborted: post-audit reload failed", "error", err)
		return c
	}
	for _, t := range titles {
		if libraryTitleHasPoster(t.Metadata) {
			continue
		}
		ev, _, ok := extractIdentityEvidence(t, firstRelease(t))
		raw, _ := json.Marshal(ev)
		fp := fingerprintIdentity(ev)
		_ = s.queueRepo.EnsureLibraryRepairEntry(ctx, t.Type, t.IMDbID, fp, string(raw))
		if !ok {
			// No usable evidence: park for review without a network call
			// and without consuming an attempt.
			_ = s.queueRepo.RecordLibraryRepairNoAttempt(ctx, t.Type, t.IMDbID,
				database.LibraryRepairReviewRequired, "empty_query", time.Now().Add(30*24*time.Hour).Unix(), fp, string(raw))
			continue
		}
	}

	// Quotas before claiming: canonical and synthetic each get half the
	// batch so synthetic history cannot starve canonical retries.
	leased, err := s.queueRepo.LeaseLibraryRepairDueQuotas(ctx, libraryRepairBatchCap/2, libraryRepairBatchCap-libraryRepairBatchCap/2, libraryRepairLeaseSeconds)
	if err != nil {
		slog.WarnContext(ctx, "Library repair pass aborted: lease failed", "error", err)
		return c
	}
	for _, e := range leased {
		if ctx.Err() != nil {
			break
		}
		token := e.LeaseToken
		done := func(status, reason string) {
			_ = s.queueRepo.UpdateLibraryRepairResultToken(ctx, e.MediaType, e.IMDbID, token, status, reason, 0, e.InputFingerprint, e.Evidence, false)
		}
		fail := func(status, reason string, nextAttempt int64, fp, raw string) {
			_ = s.queueRepo.UpdateLibraryRepairResultToken(ctx, e.MediaType, e.IMDbID, token, status, reason, nextAttempt, fp, raw, true)
		}
		c.attempted++
		t := findTitle(titles, e.MediaType, e.IMDbID)
		if t == nil {
			// Title relinked away since classification: resolve the lease.
			_ = s.queueRepo.UpdateLibraryRepairResultToken(ctx, e.MediaType, e.IMDbID, token, database.LibraryRepairResolvedIdentity, "relinked_away", 0, e.InputFingerprint, e.Evidence, false)
			c.resolved++
			continue
		}
		if libraryTitleHasPoster(t.Metadata) {
			_ = s.queueRepo.UpdateLibraryRepairResultToken(ctx, e.MediaType, e.IMDbID, token, database.LibraryRepairPosterAdded, "poster_present", 0, e.InputFingerprint, e.Evidence, false)
			c.posterAdd++
			continue
		}
		if e.Attempts >= libraryRepairMaxAttempts {
			fail(database.LibraryRepairTerminalUnresolved, "exhausted", time.Now().Add(90*24*time.Hour).Unix(), e.InputFingerprint, e.Evidence)
			c.exhausted++
			continue
		}
		isSynthetic := !strings.HasPrefix(t.IMDbID, "tt")

		if !isSynthetic {
			// Canonical path: retry existing enrichment. Wrong ID/type and
			// unusable poster URLs are rejected, not persisted.
			before := string(t.Metadata)
			if !pauseRepairNetwork(ctx) {
				fail(database.LibraryRepairWaitingRetry, "enrich_transient",
					time.Now().Add(repairBackoff(0, e.Attempts)).Unix(), e.InputFingerprint, e.Evidence)
				c.transient++
				continue
			}
			enriched := enrichLibraryTitle(ctx, database.LibraryTitle{
				Type: t.Type, IMDbID: t.IMDbID, Name: t.Name, Metadata: t.Metadata,
			})
			if !libraryTitleHasPoster(enriched.Metadata) {
				// Cinemeta is unreachable or rate-limited (it 403s/floods plain
				// HTTP bursts) — the artwork CDN serves art directly by IMDb
				// id, so fetch artwork there instead of churning rows until the
				// 8-attempt cap. Full metadata re-enrichment happens when
				// Cinemeta answers again.
				if art, ok := applyMetahubFallback(ctx, t.IMDbID, t.Type, enriched); ok {
					database.LibraryAssetMu.Lock()
					if err := s.queueRepo.UpdateLibraryTitleMetadata(ctx, t.Type, t.IMDbID, art.Name, art.Metadata); err != nil {
						database.LibraryAssetMu.Unlock()
						slog.WarnContext(ctx, "Library metahub artwork store failed", "title", t.Name, "error", err)
						c.transient++
						continue
					}
					database.LibraryAssetMu.Unlock()
					slog.InfoContext(ctx, "Library poster refreshed from artwork CDN", "title", t.Name, "id", t.IMDbID)
					_ = s.queueRepo.UpdateLibraryRepairResultToken(ctx, e.MediaType, e.IMDbID, token, database.LibraryRepairPosterAdded, "poster_metahub", 0, e.InputFingerprint, e.Evidence, false)
					c.posterAdd++
					continue
				}
				outcome := classifyRepairOutcome(true, false, false, false, false)
				c.transient++
				fail(database.LibraryRepairWaitingRetry, outcome.code,
					time.Now().Add(repairBackoff(outcome.backoff, e.Attempts)).Unix(), e.InputFingerprint, e.Evidence)
				continue
			}
			if string(enriched.Metadata) == before {
				outcome := classifyRepairOutcome(false, true, false, false, false)
				fail(database.LibraryRepairWaitingRetry, outcome.code,
					time.Now().Add(repairBackoff(outcome.backoff, e.Attempts)).Unix(), e.InputFingerprint, e.Evidence)
				c.transient++
				continue
			}
			database.LibraryAssetMu.Lock()
			err := s.queueRepo.UpdateLibraryTitleMetadata(ctx, t.Type, t.IMDbID, enriched.Name, enriched.Metadata)
			database.LibraryAssetMu.Unlock()
			if err != nil {
				slog.WarnContext(ctx, "Library poster refresh failed", "title", t.Name, "error", err)
				c.transient++
				continue
			}
			slog.InfoContext(ctx, "Library poster refreshed", "title", enriched.Name, "id", enriched.IMDbID)
			_ = s.queueRepo.UpdateLibraryRepairResultToken(ctx, e.MediaType, e.IMDbID, token, database.LibraryRepairPosterAdded, "poster_refreshed", 0, e.InputFingerprint, e.Evidence, false)
			c.posterAdd++
			continue
		}

		// Synthetic path: evidence → gated search → transactional relink →
		// canonical enrichment. Confidence gate lives in the resolver
		// (normalized title + year + unambiguous winner); here we enforce
		// no-relink-without-poster and no-relink-to-self.
		ev, query, ok := extractIdentityEvidence(*t, firstRelease(*t))
		raw, _ := json.Marshal(ev)
		fp := fingerprintIdentity(ev)
		if !ok || query == "" {
			_ = s.queueRepo.UpdateLibraryRepairResultToken(ctx, e.MediaType, e.IMDbID, token, database.LibraryRepairReviewRequired, "empty_query", time.Now().Add(30*24*time.Hour).Unix(), fp, string(raw), false)
			c.review++
			continue
		}
		// Evidence changed since claim (new parent info): release the lease
		// and re-key via Ensure (reopens the row, resets budget).
		if fp != e.InputFingerprint && e.InputFingerprint != "" {
			done(database.LibraryRepairPending, "evidence_changed")
			_ = s.queueRepo.EnsureLibraryRepairEntry(ctx, e.MediaType, e.IMDbID, fp, string(raw))
			c.deferred++
			continue
		}
		if !pauseRepairNetwork(ctx) {
			fail(database.LibraryRepairWaitingRetry, "enrich_transient",
				time.Now().Add(repairBackoff(0, e.Attempts)).Unix(), fp, string(raw))
			c.transient++
			continue
		}
		enriched := enrichLibraryTitleBySearch(ctx, database.LibraryTitle{
			Type: t.Type, IMDbID: t.IMDbID, Name: query, Metadata: t.Metadata,
		}, userAgent)
		if !strings.HasPrefix(enriched.IMDbID, "tt") || enriched.IMDbID == t.IMDbID {
			outcome := classifyRepairOutcome(false, false, false, false, false)
			fail(database.LibraryRepairWaitingRetry, outcome.code,
				time.Now().Add(repairBackoff(outcome.backoff, e.Attempts)).Unix(), fp, string(raw))
			c.transient++
			continue
		}
		if !libraryTitleHasPoster(enriched.Metadata) {
			// The relinked identity has no art from Cinemeta (or Cinemeta
			// failed entire-layer) — the artwork CDN fills the visual so the
			// identity merge still completes; without a poster the tile
			// stays broken, which defeats the relink's purpose.
			resolved := database.LibraryTitle{Type: t.Type, IMDbID: enriched.IMDbID, Name: enriched.Name, Metadata: enriched.Metadata}
			if art, ok := applyMetahubFallback(ctx, enriched.IMDbID, t.Type, resolved); ok {
				enriched.Metadata = art.Metadata
				database.LibraryAssetMu.Lock()
				err = s.queueRepo.RelinkLibraryTitle(ctx, t.Type, t.IMDbID, enriched)
				database.LibraryAssetMu.Unlock()
				if err == nil {
					slog.InfoContext(ctx, "Library title enriched from artwork CDN after resolve",
						"from", t.IMDbID, "to", enriched.IMDbID, "title", enriched.Name)
					_ = s.queueRepo.UpdateLibraryRepairResultToken(ctx, e.MediaType, e.IMDbID, token, database.LibraryRepairResolvedIdentity, "relinked", 0, e.InputFingerprint, string(raw), false)
					c.relinked++
					continue
				}
				slog.WarnContext(ctx, "Library title relink failed", "title", t.Name, "error", err)
				c.transient++
				continue
			}
			fail(database.LibraryRepairWaitingRetry, "resolved_no_artwork",
				time.Now().Add(repairBackoff(72*time.Hour, e.Attempts)).Unix(), fp, string(raw))
			c.transient++
			continue
		}
		database.LibraryAssetMu.Lock()
		err = s.queueRepo.RelinkLibraryTitle(ctx, t.Type, t.IMDbID, enriched)
		database.LibraryAssetMu.Unlock()
		if err != nil {
			slog.WarnContext(ctx, "Library title refresh failed", "title", t.Name, "error", err)
			c.transient++
			continue
		}
		slog.InfoContext(ctx, "Library title refreshed with resolved metadata",
			"from", t.IMDbID, "to", enriched.IMDbID, "title", enriched.Name)
		_ = s.queueRepo.UpdateLibraryRepairResultToken(ctx, e.MediaType, e.IMDbID, token, database.LibraryRepairResolvedIdentity, "relinked", 0, e.InputFingerprint, e.Evidence, false)
		c.relinked++
		c.resolved++
	}
	c.log(ctx)
	return c
}

// pauseRepairNetwork sleeps libraryRepairEnrichGap between enriching calls,
// unless the pass context is already done (returns false; the caller parks
// the row for the next pass instead of silently dropping it).
func pauseRepairNetwork(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(libraryRepairEnrichGap):
		return true
	}
}

func firstRelease(t database.LibraryTitle) database.LibraryRelease {
	if len(t.Releases) > 0 {
		return t.Releases[0]
	}
	return database.LibraryRelease{}
}

// auditLibraryIdentitySplits detaches mis-anchored releases before pass
// repair work. A tt-keyed title's releases are parsed with the shared
// release parser; those whose year is known and conflicts with the title's
// canonical year are re-keyed to a synthetic altm-<type>-<slug>-<year> row
// (Name "Title (YYY)"), which the synthetic repair path re-resolves through
// the year-strict Cinemeta search and the artwork-CDN fallback. Only a
// minority subset is treated as mis-anchored: a whole-title conflict means
// the title itself is mislabeled, which belongs to the relink paths, not to
// a split. No network calls happen here.
func (s *Server) auditLibraryIdentitySplits(ctx context.Context, titles []database.LibraryTitle, c *libraryRepairCounters) {
	if s.queueRepo == nil {
		return
	}
	for i := range titles {
		if ctx.Err() != nil {
			return
		}
		t := &titles[i]
		if !libraryIMDbPattern.MatchString(t.IMDbID) {
			// Synthetic rows already re-resolve through the normal path.
			continue
		}
		canonical := libraryTitleCanonicalYear(*t)
		if canonical <= 0 {
			continue
		}
		conflict := make([]database.LibraryRelease, 0, len(t.Releases))
		for _, r := range t.Releases {
			if y := libraryReleaseParsedYear(r); y > 0 && y != canonical {
				conflict = append(conflict, r)
			}
		}
		// Anchor-partition policy. The split never guesses which side is
		// right: the tt row's canonical year comes from its Cinemeta-enriched
		// metadata, so releases agreeing with canonical are metadata-anchored
		// and stay; releases with a different parsed year are re-anchored to
		// their own year key. This is what relinking cannot do: a relink
		// resolves the WHOLE title to one winner, which would re-interleave
		// the two shows (the live case was exactly half/half). The truly
		// ambiguous case — NO release agrees with canonical (no surviving
		// anchor to partition around) — is left untouched for the relink
		// paths that own whole-title relabeling.
		if len(conflict) == 0 || len(conflict) >= len(t.Releases) {
			continue
		}
		baseName := libraryTitleBaseName(t.Name)
		if baseName == "" {
			baseName = strings.TrimSpace(t.Name)
		}
		// Group conflicts by their parsed year: each foreign year gets its
		// own year-keyed synthetic anchor ("Africa (2001)" vs a second
		// merged show sharing the name would be another group).
		groups := map[int][]database.LibraryRelease{}
		for _, r := range conflict {
			groups[libraryReleaseParsedYear(r)] = append(groups[libraryReleaseParsedYear(r)], r)
		}
		years := make([]int, 0, len(groups))
		for y := range groups {
			years = append(years, y)
		}
		sort.Ints(years)
		for _, y := range years {
			if ctx.Err() != nil {
				return
			}
			members := groups[y]
			target := database.LibraryTitle{
				Type:     t.Type,
				IMDbID:   librarySyntheticIDPrefix + t.Type + "-" + librarySlug(baseName) + "-" + strconv.Itoa(y),
				Name:     baseName + " (" + strconv.Itoa(y) + ")",
				Metadata: json.RawMessage(`{"releaseInfo":` + strconv.Quote(strconv.Itoa(y)) + `,"year":` + strconv.Quote(strconv.Itoa(y)) + `,"split_from":` + strconv.Quote(t.IMDbID) + `}`),
			}
			ids := make([]int64, 0, len(members))
			for _, r := range members {
				ids = append(ids, r.ID)
			}
			if err := s.queueRepo.SplitLibraryReleases(ctx, t.Type, t.IMDbID, target, ids); err != nil {
				slog.WarnContext(ctx, "Library identity split failed", "title", t.Name, "error", err)
				continue
			}
			ev, _, _ := extractIdentityEvidence(target, members[0])
			raw, _ := json.Marshal(ev)
			_ = s.queueRepo.EnsureLibraryRepairEntry(ctx, t.Type, target.IMDbID, fingerprintIdentity(ev), string(raw))
			c.split += len(ids)
			slog.InfoContext(ctx, "Library identity split",
				"title", t.Name, "kept", t.IMDbID, "detached", target.IMDbID, "releases", len(ids))
		}
	}
}

// findTitle resolves a leased repair row to its current title snapshot.
func findTitle(titles []database.LibraryTitle, mediaType, imdbID string) *database.LibraryTitle {
	for i := range titles {
		if titles[i].Type == mediaType && titles[i].IMDbID == imdbID {
			return &titles[i]
		}
	}
	return nil
}
