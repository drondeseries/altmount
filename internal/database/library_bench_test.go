package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// BenchmarkLibraryTitlesAgainstRealDB measures LibraryTitles + the per-title
// enrichment loop against a copy of a production database. It skips unless
// ALMOUNT_BENCH_DB points at a DB copy, so CI stays green.
func benchDB(tb testing.TB) *Repository {
	tb.Helper()
	path := os.Getenv("ALMOUNT_BENCH_DB")
	if path == "" {
		tb.Skip("set ALMOUNT_BENCH_DB to a database copy to run this benchmark")
	}
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { db.Close() })
	return NewRepository(db, DialectSQLite)
}

func BenchmarkLibraryTitlesRealDB(b *testing.B) {
	repo := benchDB(b)
	ctx := context.Background()
	if _, err := repo.LibraryTitles(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := repo.LibraryTitles(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLibraryListEnrichRealDB mirrors handleLibraryList: titles plus
// batched enrichment across the whole library.
func BenchmarkLibraryListEnrichRealDB(b *testing.B) {
	repo := benchDB(b)
	ctx := context.Background()
	if _, err := repo.LibraryTitles(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		titles, err := repo.LibraryTitles(ctx)
		if err != nil {
			b.Fatal(err)
		}
		for j := range titles {
			annotateOriginForBench(titles[j].Releases)
		}
		repo.EnrichLibraryTitles(ctx, titles)
	}
}

// BenchmarkLibraryListPerTitleRealDB keeps the old per-title call shape so the
// N+1 regression stays measurable.
func BenchmarkLibraryListPerTitleRealDB(b *testing.B) {
	repo := benchDB(b)
	ctx := context.Background()
	if _, err := repo.LibraryTitles(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		titles, err := repo.LibraryTitles(ctx)
		if err != nil {
			b.Fatal(err)
		}
		for j := range titles {
			repo.EnrichLibraryReleases(ctx, titles[j].Releases)
		}
	}
}

func annotateOriginForBench(releases []LibraryRelease) {
	for i := range releases {
		if releases[i].DownloadID != "" && strings.HasPrefix(releases[i].DownloadID, "stremio:") {
			continue
		}
	}
}
