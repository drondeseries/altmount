package cmd

import (
	"compress/gzip"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/kipsilabs/altmount/internal/config"
)

// TestCreateFiberAppCompressesJSON pins the wire behavior that made the saved
// library feel slow: the /api/library JSON body is ~68MB for a large library
// and used to ship verbatim. This drives the real createFiberApp stack (not a
// copy of the middleware config) so a future reorder or removal of the
// compression middleware fails here.
func TestCreateFiberAppCompressesJSON(t *testing.T) {
	cfg := &config.Config{}
	app, _ := createFiberApp(context.Background(), cfg)
	app.Get("/api/library", func(c *fiber.Ctx) error {
		items := make([]map[string]string, 4000)
		for i := range items {
			items[i] = map[string]string{
				"overview": "A long episode overview that repeats a lot of prose. " +
					"A long episode overview that repeats a lot of prose.",
			}
		}
		return c.JSON(fiber.Map{"data": items})
	})

	request := httptest.NewRequest("GET", "/api/library", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if got := response.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (compression middleware missing from app stack)", got)
	}
	if !strings.Contains(response.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary = %q, want it to include Accept-Encoding", response.Header.Get("Vary"))
	}

	// A raw (uncompressed) JSON body would not be gzip-decodable.
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatalf("response body must be gzip-encoded: %v", err)
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
}
