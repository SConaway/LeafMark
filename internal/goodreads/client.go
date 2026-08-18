// Package goodreads talks to Goodreads' undocumented, session-cookie-auth
// web app (there is no official API — see the project README for why).
// Every request shape here was confirmed against a real captured HAR, not
// guessed from documentation that doesn't exist.
package goodreads

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
)

const defaultBaseURL = "https://www.goodreads.com"

// ShelfName is a Goodreads shelf slug, as used in the review/list_rss query
// string.
type ShelfName string

const (
	ShelfWantToRead       ShelfName = "to-read"
	ShelfCurrentlyReading ShelfName = "currently-reading"
)

// ShelfBook is one book on a fetched shelf.
type ShelfBook struct {
	GoodreadsBookID string
	Title           string
	Author          string
}

// Goodreads is the interface the rest of LeafMark depends on, so the blast
// radius of Goodreads changing something stays contained to this package.
type Goodreads interface {
	FetchShelf(ctx context.Context, shelf ShelfName) ([]ShelfBook, error)
	UpdateProgress(ctx context.Context, bookID string, percent int) error
}

// Client is a Goodreads client authenticated via a harvested browser cookie
// jar (see login.go for how that cookie is obtained/refreshed).
type Client struct {
	userID     string
	baseURL    string // overridable in tests; defaultBaseURL in production
	httpClient *http.Client

	mu        sync.Mutex
	cookie    string // full raw "Cookie:" header value; empty until logged in
	csrfToken string
}

// NewClient constructs a Client for the given Goodreads numeric user ID.
// It starts unauthenticated — call Login (login.go) before making requests,
// or let the poll loop's lazy re-login-on-ErrSessionInvalid handle it.
func NewClient(userID string) *Client {
	return &Client{
		userID:     userID,
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{},
	}
}

// SetCookie installs a freshly harvested cookie jar (the full raw "Cookie:"
// header string) and clears any cached CSRF token, since a new session
// invalidates it.
func (c *Client) SetCookie(cookie string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cookie = cookie
	c.csrfToken = ""
	log.Printf("goodreads: installed a new cookie jar (%d bytes, %d cookies) and cleared the cached CSRF token", len(cookie), strings.Count(cookie, "=")) // count is approximate, purely diagnostic
}

func (c *Client) currentCookie() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cookie
}

// newRequest builds a request against Goodreads with the current cookie jar
// attached.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if cookie := c.currentCookie(); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	return req, nil
}

// looksLikeSignInRedirect reports whether a response indicates the session
// is no longer valid: a redirect (or a 200 that already landed on) Amazon's
// or Goodreads' sign-in pages.
func looksLikeSignInRedirect(resp *http.Response) bool {
	loc := resp.Header.Get("Location")
	if loc != "" && (strings.Contains(loc, "/ap/signin") || strings.Contains(loc, "/user/sign_in")) {
		log.Printf("goodreads: response to %s carried a Location header pointing at sign-in (%q) — treating session as invalid", requestURL(resp), loc)
		return true
	}
	if resp.Request != nil && resp.Request.URL != nil {
		p := resp.Request.URL.Path
		if strings.Contains(p, "/ap/signin") || strings.Contains(p, "/user/sign_in") {
			log.Printf("goodreads: request ended up on sign-in page %q instead of the intended endpoint — treating session as invalid", p)
			return true
		}
	}
	return false
}

func requestURL(resp *http.Response) string {
	if resp.Request == nil || resp.Request.URL == nil {
		return "?"
	}
	return resp.Request.URL.String()
}
