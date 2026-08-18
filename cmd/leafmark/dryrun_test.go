package main

import (
	"context"
	"testing"

	"leafmark/internal/goodreads"
)

// spyGoodreads records whether its UpdateProgress (the real push, which
// would perform the actual POST /user_status.json) was ever called.
type spyGoodreads struct {
	updateCalled bool
}

func (s *spyGoodreads) FetchShelf(ctx context.Context, shelf goodreads.ShelfName) ([]goodreads.ShelfBook, error) {
	return nil, nil
}

func (s *spyGoodreads) UpdateProgress(ctx context.Context, bookID string, percent int) error {
	s.updateCalled = true
	return nil
}

func TestDryRunGoodreadsNeverCallsRealUpdateProgress(t *testing.T) {
	spy := &spyGoodreads{}
	d := dryRunGoodreads{Goodreads: spy}

	if err := d.UpdateProgress(context.Background(), "40605629", 85); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	if spy.updateCalled {
		t.Fatal("dryRunGoodreads.UpdateProgress must never call through to the wrapped client's UpdateProgress — that's the real POST /user_status.json")
	}
}

func TestDryRunGoodreadsPassesFetchShelfThrough(t *testing.T) {
	spy := &spyGoodreads{}
	d := dryRunGoodreads{Goodreads: spy}

	// FetchShelf is read-only (a public, unauthenticated RSS GET) and
	// deliberately not overridden — matching needs real shelf data to be a
	// meaningful dry run.
	if _, err := d.FetchShelf(context.Background(), goodreads.ShelfWantToRead); err != nil {
		t.Fatalf("FetchShelf: %v", err)
	}
}
