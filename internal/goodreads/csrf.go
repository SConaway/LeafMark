package goodreads

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

	"golang.org/x/net/html"
)

// ensureCSRFToken returns the cached CSRF token, fetching a fresh one from
// an authenticated Goodreads page if there isn't one cached yet. The token
// is scraped from a <meta name="csrf-token" content="..."> tag rather than
// hand-built, since it's a Rails-issued value tied to the session.
func (c *Client) ensureCSRFToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	token := c.csrfToken
	c.mu.Unlock()
	if token != "" {
		log.Print("goodreads: using cached CSRF token")
		return token, nil
	}
	log.Print("goodreads: no cached CSRF token, fetching a fresh one")
	return c.fetchCSRFToken(ctx)
}

// invalidateCSRFToken clears the cached token, forcing the next
// ensureCSRFToken call to fetch a fresh one.
func (c *Client) invalidateCSRFToken() {
	c.mu.Lock()
	c.csrfToken = ""
	c.mu.Unlock()
	log.Print("goodreads: invalidated cached CSRF token")
}

func (c *Client) fetchCSRFToken(ctx context.Context) (string, error) {
	path := "/user/show/" + c.userID
	log.Printf("goodreads: GET %s%s (scraping csrf-token meta tag)", c.baseURL, path)

	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("goodreads: csrf page request failed: %v", err)
		return "", fmt.Errorf("goodreads: fetch csrf page: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("goodreads: csrf page -> HTTP %d", resp.StatusCode)

	if looksLikeSignInRedirect(resp) {
		return "", ErrSessionInvalid
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("goodreads: unexpected status %d fetching csrf page at %s — is GOODREADS_USER_ID correct?", resp.StatusCode, path)
		return "", fmt.Errorf("goodreads: unexpected status %d fetching csrf page", resp.StatusCode)
	}

	token, err := scrapeCSRFToken(resp.Body)
	if err != nil {
		log.Printf("goodreads: failed to scrape csrf-token meta tag: %v", err)
		return "", err
	}
	if token == "" {
		// A 200 with no csrf-token meta tag at all almost always means we
		// actually landed on a signed-out page (e.g. a generic error page)
		// rather than a real CSRF-shaped failure.
		log.Print("goodreads: no csrf-token meta tag found on a 200 response — treating as a signed-out page")
		return "", ErrSessionInvalid
	}

	c.mu.Lock()
	c.csrfToken = token
	c.mu.Unlock()

	log.Printf("goodreads: fetched a fresh CSRF token (%d bytes)", len(token))
	return token, nil
}

// scrapeCSRFToken extracts the content of <meta name="csrf-token"
// content="..."> from an HTML document. Uses a real tokenizer rather than
// regex, since Rails' quote-escaping in this tag isn't reliably regex-safe.
func scrapeCSRFToken(r io.Reader) (string, error) {
	z := html.NewTokenizer(r)
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if err := z.Err(); err != io.EOF {
				return "", err
			}
			return "", nil
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "meta" || !hasAttr {
				continue
			}
			var isCSRF bool
			var content string
			for {
				key, val, more := z.TagAttr()
				switch string(key) {
				case "name":
					if string(val) == "csrf-token" {
						isCSRF = true
					}
				case "content":
					content = string(val)
				}
				if !more {
					break
				}
			}
			if isCSRF {
				return content, nil
			}
		}
	}
}
