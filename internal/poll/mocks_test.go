package poll

import (
	"context"
	"errors"
	"sync"

	"leafmark/internal/goodreads"
	"leafmark/internal/koreader"
	"leafmark/internal/ntfy"
)

type mockKOReader struct {
	doc koreader.Document
	ok  bool
	err error
}

func (m *mockKOReader) LastSyncedDocument(ctx context.Context) (koreader.Document, bool, error) {
	return m.doc, m.ok, m.err
}

type updateCall struct {
	BookID  string
	Percent int
}

type mockGoodreads struct {
	mu sync.Mutex

	shelves map[goodreads.ShelfName][]goodreads.ShelfBook

	// updateErrs is consumed in order, one per UpdateProgress call; the
	// last entry repeats once exhausted. nil entries mean success.
	updateErrs []error
	updateIdx  int
	calls      []updateCall
}

func (m *mockGoodreads) FetchShelf(ctx context.Context, shelf goodreads.ShelfName) ([]goodreads.ShelfBook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shelves[shelf], nil
}

func (m *mockGoodreads) UpdateProgress(ctx context.Context, bookID string, percent int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, updateCall{bookID, percent})

	if len(m.updateErrs) == 0 {
		return nil
	}
	idx := m.updateIdx
	if idx >= len(m.updateErrs) {
		idx = len(m.updateErrs) - 1
	} else {
		m.updateIdx++
	}
	return m.updateErrs[idx]
}

func (m *mockGoodreads) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *mockGoodreads) lastCall() updateCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[len(m.calls)-1]
}

type mockNotifier struct {
	mu            sync.Mutex
	notifications []ntfy.Notification
}

func (m *mockNotifier) Publish(ctx context.Context, n ntfy.Notification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications = append(m.notifications, n)
	return nil
}

func (m *mockNotifier) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.notifications)
}

func (m *mockNotifier) last() ntfy.Notification {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notifications[len(m.notifications)-1]
}

var errRelogin = errors.New("relogin failed")
