package httpclient

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestValidateDownloadURL pins which indexer-supplied download targets are
// allowed. Indexer search responses drive these fetches, so a hostile indexer
// must not be able to aim AltMount at loopback, private or link-local
// addresses — cloud instance-metadata endpoints in particular.
func TestValidateDownloadURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"public https host", "https://indexer.example.com/getnzb/abc?apikey=k", false},
		{"public http host", "http://indexer.example.com/getnzb/abc", false},
		{"public literal IP", "https://93.184.216.34/getnzb/abc", false},

		{"empty", "", true},
		{"no scheme", "indexer.example.com/getnzb", true},
		{"file scheme", "file:///etc/passwd", true},
		{"gopher scheme", "gopher://example.com/", true},
		{"no host", "http:///getnzb/abc", true},

		{"loopback IPv4", "http://127.0.0.1:8080/getnzb", true},
		{"loopback IPv6", "http://[::1]/getnzb", true},
		{"localhost", "http://localhost:9696/getnzb", true},
		{"localhost subdomain", "http://indexer.localhost/getnzb", true},
		{"unspecified address", "http://0.0.0.0/getnzb", true},
		{"private 10.x", "http://10.0.0.5/getnzb", true},
		{"private 192.168.x", "http://192.168.1.10/getnzb", true},
		{"private 172.16.x", "http://172.16.4.4/getnzb", true},
		{"link-local metadata endpoint", "http://169.254.169.254/latest/meta-data/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDownloadURL(tt.raw)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateDownloadURL(%q) = nil, want error", tt.raw)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateDownloadURL(%q) = %v, want nil", tt.raw, err)
			}
		})
	}
}

// TestRedactURLError pins that redaction removes credentials from a *url.Error
// while preserving the wrapped cause and the diagnosable parts of the URL.
func TestRedactURLError(t *testing.T) {
	t.Run("nil passes through", func(t *testing.T) {
		if got := RedactURLError(nil); got != nil {
			t.Fatalf("RedactURLError(nil) = %v, want nil", got)
		}
	})

	t.Run("non-url error is returned unchanged", func(t *testing.T) {
		err := errors.New("plain failure")
		if got := RedactURLError(err); got != err {
			t.Fatalf("RedactURLError(%v) = %v, want the same error", err, got)
		}
	})

	t.Run("query, fragment and userinfo are stripped", func(t *testing.T) {
		cause := errors.New("connection refused")
		err := RedactURLError(&url.Error{
			Op:  "Get",
			URL: "https://user:pw@indexer.example.com/api?apikey=SECRET&t=search#frag",
			Err: cause,
		})

		msg := err.Error()
		for _, secret := range []string{"SECRET", "apikey", "user:pw", "frag"} {
			if strings.Contains(msg, secret) {
				t.Errorf("redacted error still contains %q: %v", secret, err)
			}
		}
		if !strings.Contains(msg, "indexer.example.com/api") {
			t.Errorf("redaction dropped the diagnosable host/path: %v", err)
		}
		if !errors.Is(err, cause) {
			t.Errorf("redaction broke the wrapped cause chain")
		}
	})

	t.Run("unparseable url is dropped wholesale", func(t *testing.T) {
		err := RedactURLError(&url.Error{
			Op:  "Get",
			URL: "http://[::1:80/api?apikey=SECRET",
			Err: errors.New("boom"),
		})
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("unparseable URL leaked its query: %v", err)
		}
	})
}

func TestSafeDownloadCheckRedirect(t *testing.T) {
	checkRedirect := SafeDownloadCheckRedirect(3)

	t.Run("allows public redirect", func(t *testing.T) {
		target, _ := url.Parse("https://cdn.example.com/dl.nzb")
		prev, _ := url.Parse("https://indexer.example.com/get")
		req := &http.Request{URL: target, Header: make(http.Header)}
		req.Header.Set("X-Api-Key", "prowlarr-secret")
		req.Header.Set("Authorization", "Bearer secret")
		via := []*http.Request{{URL: prev}}

		err := checkRedirect(req, via)
		if err != nil {
			t.Fatalf("expected nil, got error: %v", err)
		}
		// Cross-domain redirect must strip sensitive credentials
		if req.Header.Get("X-Api-Key") != "" {
			t.Errorf("expected X-Api-Key to be stripped on cross-domain redirect")
		}
		if req.Header.Get("Authorization") != "" {
			t.Errorf("expected Authorization to be stripped on cross-domain redirect")
		}
	})

	t.Run("preserves headers on same-host redirect", func(t *testing.T) {
		target, _ := url.Parse("https://indexer.example.com/final.nzb")
		prev, _ := url.Parse("https://indexer.example.com/get")
		req := &http.Request{URL: target, Header: make(http.Header)}
		req.Header.Set("X-Api-Key", "my-key")
		via := []*http.Request{{URL: prev}}

		err := checkRedirect(req, via)
		if err != nil {
			t.Fatalf("expected nil, got error: %v", err)
		}
		if req.Header.Get("X-Api-Key") != "my-key" {
			t.Errorf("expected X-Api-Key to be preserved on same-host redirect")
		}
	})

	t.Run("restores key on return to original origin (A->B->A)", func(t *testing.T) {
		// Regression: net/http copies the initial request's headers into
		// EVERY redirect request before CheckRedirect runs, so binding the
		// credential strip to the previous hop (prev == req host) leaks the
		// key back on the second hop. Binding to via[0] fixes it.
		a1, _ := url.Parse("https://prowlarr.example.com/api/download?id=1")
		b, _ := url.Parse("https://cdn.example.org/file.nzb")
		a2, _ := url.Parse("https://prowlarr.example.com/log?hit=1")
		via := []*http.Request{{URL: a1}, {URL: b}}

		req := &http.Request{URL: b, Header: make(http.Header)}
		req.Header.Set("X-Api-Key", "prowlarr-secret")
		if err := checkRedirect(req, via[:1]); err != nil {
			t.Fatalf("hop A->B: expected nil, got %v", err)
		}
		if req.Header.Get("X-Api-Key") != "" {
			t.Fatalf("hop A->B: expected X-Api-Key stripped")
		}

		// Simulate net/http copying the ORIGINAL headers onto hop 2
		// (req.Header starts fresh from the initial request each hop).
		req2 := &http.Request{URL: a2, Header: make(http.Header)}
		req2.Header.Set("X-Api-Key", "prowlarr-secret")
		if err := checkRedirect(req2, via); err != nil {
			t.Fatalf("hop A->B->A: expected nil, got %v", err)
		}
		if req2.Header.Get("X-Api-Key") != "prowlarr-secret" {
			t.Errorf("hop A->B->A: expected X-Api-Key restored on return to original origin, got %q", req2.Header.Get("X-Api-Key"))
		}
	})

	t.Run("strips key on every hop of A->B->B chain", func(t *testing.T) {
		// The exact regression: Go recopies the INITIAL request's headers
		// into every redirect request before CheckRedirect runs, so a
		// prev-hop comparison would see prev == req host on the second B
		// hop and leak the key. via[0]-binding strips on every off-origin
		// hop.
		a, _ := url.Parse("https://prowlarr.example.com/api/download?id=1")
		b1, _ := url.Parse("https://cdn.example.org/file.nzb")
		b2, _ := url.Parse("https://cdn.example.org/file.nzb?part=2")
		via := []*http.Request{{URL: a}, {URL: b1}}

		req1 := &http.Request{URL: b1, Header: make(http.Header)}
		req1.Header.Set("X-Api-Key", "prowlarr-secret")
		if err := checkRedirect(req1, via[:1]); err != nil {
			t.Fatalf("hop A->B1: expected nil, got %v", err)
		}
		if req1.Header.Get("X-Api-Key") != "" {
			t.Fatalf("hop A->B1: expected X-Api-Key stripped")
		}

		// Second hop still off-origin: headers re-copied from initial req.
		req2 := &http.Request{URL: b2, Header: make(http.Header)}
		req2.Header.Set("X-Api-Key", "prowlarr-secret")
		req2.Header.Set("Authorization", "Bearer secret")
		if err := checkRedirect(req2, via); err != nil {
			t.Fatalf("hop A->B1->B2: expected nil, got %v", err)
		}
		if req2.Header.Get("X-Api-Key") != "" {
			t.Errorf("hop A->B1->B2: X-Api-Key leaked on second off-origin hop")
		}
		if req2.Header.Get("Authorization") != "" {
			t.Errorf("hop A->B1->B2: Authorization leaked on second off-origin hop")
		}
	})

	t.Run("strips key on port change", func(t *testing.T) {
		target, _ := url.Parse("https://indexer.example.com:8443/final.nzb")
		prev, _ := url.Parse("https://indexer.example.com/get")
		req := &http.Request{URL: target, Header: make(http.Header)}
		req.Header.Set("Authorization", "Bearer secret")
		via := []*http.Request{{URL: prev}}

		if err := checkRedirect(req, via); err != nil {
			t.Fatalf("expected nil, got error: %v", err)
		}
		if req.Header.Get("Authorization") != "" {
			t.Errorf("expected Authorization stripped on port change")
		}
	})

	t.Run("blocks redirect to private ip", func(t *testing.T) {
		target, _ := url.Parse("http://192.168.1.1/admin")
		prev, _ := url.Parse("https://indexer.example.com/get")
		req := &http.Request{URL: target}
		via := []*http.Request{{URL: prev}}

		err := checkRedirect(req, via)
		if err == nil {
			t.Fatal("expected error redirecting to private ip, got nil")
		}
	})

	t.Run("blocks redirect to loopback", func(t *testing.T) {
		target, _ := url.Parse("http://127.0.0.1:8080/secret")
		prev, _ := url.Parse("https://indexer.example.com/get")
		req := &http.Request{URL: target}
		via := []*http.Request{{URL: prev}}

		err := checkRedirect(req, via)
		if err == nil {
			t.Fatal("expected error redirecting to loopback, got nil")
		}
	})

	t.Run("blocks redirect exceeding limit", func(t *testing.T) {
		target, _ := url.Parse("https://cdn.example.com/dl.nzb")
		prev, _ := url.Parse("https://indexer.example.com/get")
		req := &http.Request{URL: target}
		via := []*http.Request{{URL: prev}, {URL: prev}, {URL: prev}}

		err := checkRedirect(req, via)
		if err == nil {
			t.Fatal("expected error when redirect limit exceeded, got nil")
		}
	})
}
