package goodreads

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const csrfPageHTML = `<!DOCTYPE html><html><head>
<meta charset="utf-8">
<meta name="csrf-param" content="authenticity_token" />
<meta name="csrf-token" content="abc123token==" />
</head><body>hi</body></html>`

func TestEnsureCSRFTokenScrapesAndCaches(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte(csrfPageHTML))
	})

	token, err := c.ensureCSRFToken(context.Background())
	if err != nil {
		t.Fatalf("ensureCSRFToken: %v", err)
	}
	if token != "abc123token==" {
		t.Fatalf("token = %q", token)
	}

	// Second call should hit the cache, not the server again.
	if _, err := c.ensureCSRFToken(context.Background()); err != nil {
		t.Fatalf("second ensureCSRFToken: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected 1 request (cached second time), got %d", hits)
	}

	c.invalidateCSRFToken()
	if _, err := c.ensureCSRFToken(context.Background()); err != nil {
		t.Fatalf("ensureCSRFToken after invalidate: %v", err)
	}
	if hits != 2 {
		t.Fatalf("expected 2 requests after invalidate, got %d", hits)
	}
}

func TestEnsureCSRFTokenSignInRedirect(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/sign_in" {
			w.Write([]byte(`<html><body>please sign in</body></html>`))
			return
		}
		http.Redirect(w, r, "/user/sign_in", http.StatusFound)
	})

	_, err := c.ensureCSRFToken(context.Background())
	if err != ErrSessionInvalid {
		t.Fatalf("expected ErrSessionInvalid, got %v", err)
	}
}

func TestEnsureCSRFTokenMissingMetaTagIsSessionInvalid(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>no csrf tag here</body></html>`))
	})

	_, err := c.ensureCSRFToken(context.Background())
	if err != ErrSessionInvalid {
		t.Fatalf("expected ErrSessionInvalid, got %v", err)
	}
}

func TestScrapeCSRFToken(t *testing.T) {
	token, err := scrapeCSRFToken(strings.NewReader(csrfPageHTML))
	if err != nil {
		t.Fatalf("scrapeCSRFToken: %v", err)
	}
	if token != "abc123token==" {
		t.Fatalf("token = %q", token)
	}
}
