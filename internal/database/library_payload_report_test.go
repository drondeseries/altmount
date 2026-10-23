package database

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestLibraryPayloadSizeReport prints the JSON payload size the /api/library
// endpoint ships for a real database copy. Informational: helps spot payload
// blowups that make the page feel slow.
func TestLibraryPayloadSizeReport(t *testing.T) {
	path := os.Getenv("ALMOUNT_BENCH_DB")
	if path == "" {
		t.Skip("set ALMOUNT_BENCH_DB to a database copy to run this report")
	}
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db, DialectSQLite)
	titles, err := repo.LibraryTitles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	repo.EnrichLibraryTitles(context.Background(), titles)
	total := 0
	minified, err := json.Marshal(titles)
	if err != nil {
		t.Fatal(err)
	}
	for i := range titles {
		total += len(titles[i].Releases)
	}
	// Field-level breakdown: marshal each field group alone to see what dominates.
	metaBytes, relBytes := 0, 0
	for i := range titles {
		metaBytes += len(titles[i].Metadata)
	}
	for _, group := range []struct {
		name string
		val  any
	}{
		{"releases", func() any {
			out := make([][]LibraryRelease, 0, len(titles))
			for i := range titles {
				out = append(out, titles[i].Releases)
			}
			return out
		}()},
		{"snapshots", func() any {
			out := make([]any, 0, total)
			for i := range titles {
				for j := range titles[i].Releases {
					out = append(out, titles[i].Releases[j].Snapshot)
				}
			}
			return out
		}()},
		{"names", func() any {
			out := make([]string, 0, total)
			for i := range titles {
				for j := range titles[i].Releases {
					out = append(out, titles[i].Releases[j].Name)
				}
			}
			return out
		}()},
	} {
		b, err := json.Marshal(group.val)
		if err != nil {
			t.Fatal(err)
		}
		relBytes += len(b)
		t.Logf("  %-10s %8.2f MB", group.name, float64(len(b))/1024/1024)
	}
	t.Logf("titles=%d releases=%d json=%d bytes (%.1f MB)",
		len(titles), total, len(minified), float64(len(minified))/1024/1024)
	t.Logf("title metadata total=%.2f MB", float64(metaBytes)/1024/1024)
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if _, err := zw.Write(minified); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	t.Logf("gzip(BestSpeed) of full payload = %.2f MB (%.1fx)", float64(buf.Len())/1024/1024,
		float64(len(minified))/float64(buf.Len()))
}
