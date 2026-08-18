package web

import (
	"context"
	"sync"

	"leafmark/internal/goodreads"
)

// mockGoodreads is a minimal in-memory stand-in for goodreads.Goodreads so
// confirm-endpoint tests don't touch the network.
type mockGoodreads struct {
	mu sync.Mutex

	shelves map[goodreads.ShelfName][]goodreads.ShelfBook

	updateErr   error
	updateCalls []updateProgressCall
}

type updateProgressCall struct {
	BookID  string
	Percent int
}

func (m *mockGoodreads) FetchShelf(ctx context.Context, shelf goodreads.ShelfName) ([]goodreads.ShelfBook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shelves[shelf], nil
}

func (m *mockGoodreads) UpdateProgress(ctx context.Context, bookID string, percent int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCalls = append(m.updateCalls, updateProgressCall{bookID, percent})
	return m.updateErr
}

func (m *mockGoodreads) calls() []updateProgressCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]updateProgressCall, len(m.updateCalls))
	copy(out, m.updateCalls)
	return out
}
