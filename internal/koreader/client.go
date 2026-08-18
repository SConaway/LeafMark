// Package koreader talks to the koreader-sync server (gh:nperez0111/koreader-sync)
// that KOReader devices push reading progress to. Contract confirmed by
// reading that server's source directly, not guessed from KOReader's
// general Progress-sync protocol docs.
package koreader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
)

// Document is the last-synced document KOReader reported, with progress
// normalized to an integer 0-100 percent (the server stores it as a
// 0.0-1.0 fraction, per KOReader's standard Progress-sync protocol;
// rounding here also avoids spurious re-pushes from float jitter and
// matches Goodreads' own integer-percent contract).
type Document struct {
	DocHash string // koreader-sync's "document" field
	Title   string
	Author  string
	Percent int
}

// KOReader is the interface the rest of LeafMark depends on.
type KOReader interface {
	// LastSyncedDocument returns the most recently updated document for
	// the authenticated user, or ok=false if the user has no synced
	// documents at all yet.
	LastSyncedDocument(ctx context.Context) (doc Document, ok bool, err error)
}

// syncedDocument mirrors one entry in GET /syncs/documents' response.
type syncedDocument struct {
	Document   string  `json:"document"`
	Percentage float64 `json:"percentage"`
	Filename   *string `json:"filename"`
	Title      *string `json:"title"`
	Authors    *string `json:"authors"`
	Timestamp  int64   `json:"timestamp"`
}

type documentsResponse struct {
	Documents []syncedDocument `json:"documents"`
}

// Client is a koreader-sync client authenticated via the per-request
// X-Auth-User / X-Auth-Key headers that server's authMiddleware expects.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// NewClient constructs a Client. baseURL should not have a trailing slash.
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:    baseURL,
		username:   username,
		password:   password,
		httpClient: &http.Client{},
	}
}

// LastSyncedDocument fetches GET /syncs/documents (sorted by the server as
// timestamp DESC) and returns the first entry — koreader-sync already does
// the "most recent" ordering for us, so there's no need to track a set of
// known doc hashes client-side.
func (c *Client) LastSyncedDocument(ctx context.Context) (Document, bool, error) {
	url := c.baseURL + "/syncs/documents"
	log.Printf("koreader: GET %s (X-Auth-User=%q, X-Auth-Key=%d bytes)", url, c.username, len(c.password))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Document{}, false, err
	}
	req.Header.Set("X-Auth-User", c.username)
	req.Header.Set("X-Auth-Key", c.password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("koreader: request to %s failed: %v", url, err)
		return Document{}, false, fmt.Errorf("koreader: fetch documents: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("koreader: %s -> HTTP %d", url, resp.StatusCode)

	if resp.StatusCode == http.StatusUnauthorized {
		log.Printf("koreader: authentication rejected for X-Auth-User=%q — this means the username/password in KOREADER_SYNC_USERNAME/KOREADER_SYNC_PASSWORD don't match what's actually registered on the koreader-sync server at %s (not a LeafMark bug — verify independently with curl if unsure)", c.username, c.baseURL)
		return Document{}, false, fmt.Errorf("koreader: authentication rejected (check KOREADER_SYNC_USERNAME/PASSWORD)")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		log.Printf("koreader: unexpected status %d from %s, body: %s", resp.StatusCode, url, body)
		return Document{}, false, fmt.Errorf("koreader: fetch documents: unexpected status %d", resp.StatusCode)
	}

	var out documentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Printf("koreader: failed to decode response from %s: %v", url, err)
		return Document{}, false, fmt.Errorf("koreader: decode documents response: %w", err)
	}

	log.Printf("koreader: authenticated OK, %d synced document(s) on record", len(out.Documents))
	if len(out.Documents) == 0 {
		return Document{}, false, nil
	}

	d := out.Documents[0]
	doc := Document{
		DocHash: d.Document,
		Title:   derefOr(d.Title, ""),
		Author:  derefOr(d.Authors, ""),
		Percent: fractionToPercent(d.Percentage),
	}
	log.Printf("koreader: most recent document: %q by %q, doc_hash=%s, %d%%", doc.Title, doc.Author, doc.DocHash, doc.Percent)
	return doc, true, nil
}

func derefOr(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

// fractionToPercent converts koreader-sync's stored 0.0-1.0 progress
// fraction to a rounded integer percent.
func fractionToPercent(fraction float64) int {
	return int(math.Round(fraction * 100))
}
