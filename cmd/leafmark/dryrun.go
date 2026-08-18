package main

import (
	"context"
	"log"

	"leafmark/internal/goodreads"
	"leafmark/internal/ntfy"
)

// dryRunGoodreads passes FetchShelf through to the real client (matching
// needs real shelf data to be a meaningful test) but logs instead of
// actually pushing progress.
type dryRunGoodreads struct {
	goodreads.Goodreads
}

func (d dryRunGoodreads) UpdateProgress(ctx context.Context, bookID string, percent int) error {
	log.Printf("[dry-run] would push progress: goodreads_book_id=%s percent=%d", bookID, percent)
	return nil
}

// dryRunNotifier logs instead of publishing to ntfy.
type dryRunNotifier struct{}

func (dryRunNotifier) Publish(ctx context.Context, n ntfy.Notification) error {
	log.Printf("[dry-run] would publish ntfy notification: title=%q message=%q actions=%d", n.Title, n.Message, len(n.Actions))
	return nil
}
