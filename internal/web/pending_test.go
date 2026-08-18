package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"leafmark/internal/db"
	"leafmark/internal/goodreads"
)

func TestPendingListShowsOpenMatches(t *testing.T) {
	s, database, _ := newTestServer(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Project Hail Mary", "Andy Weir"); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	if _, err := db.CreatePendingMatch(ctx, database, "doc-1", 42, nil); err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pending", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Project Hail Mary") {
		t.Errorf("expected page to mention the pending document's title, got:\n%s", rec.Body.String())
	}
}

func TestPendingListEmpty(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/pending", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Nothing waiting") {
		t.Errorf("expected an empty-state message, got:\n%s", rec.Body.String())
	}
}

func TestPendingDetailShowsCandidatesAndManualForm(t *testing.T) {
	s, database, _ := newTestServer(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Project Hail Mary", "Andy Weir"); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	pm, err := db.CreatePendingMatch(ctx, database, "doc-1", 42, []db.Candidate{
		{GoodreadsBookID: "40605629", Title: "Project Hail Mary", Author: "Andy Weir", Score: 0.95},
	})
	if err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pending/"+strconv.FormatInt(pm.ID, 10), nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "40605629") {
		t.Errorf("expected the cached candidate's book id to appear, got:\n%s", body)
	}
	if !strings.Contains(body, `name="goodreads_book_id"`) || !strings.Contains(body, "Confirm this ID") {
		t.Errorf("expected the manual book-id paste form to be present, got:\n%s", body)
	}
}

func TestPendingDetailUnknownIDIs404(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/pending/99999", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPendingDetailAlreadyResolvedShowsStatus(t *testing.T) {
	s, database, _ := newTestServer(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	pm, err := db.CreatePendingMatch(ctx, database, "doc-1", 10, nil)
	if err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}
	if _, err := db.ResolvePendingMatch(ctx, database, pm.ID); err != nil {
		t.Fatalf("ResolvePendingMatch: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/pending/"+strconv.FormatInt(pm.ID, 10), nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Already resolved") {
		t.Errorf("expected an already-resolved message, got:\n%s", rec.Body.String())
	}
}

func TestPendingDetailSearchQueriesBothShelves(t *testing.T) {
	s, database, gr := newTestServer(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	pm, err := db.CreatePendingMatch(ctx, database, "doc-1", 10, nil)
	if err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}

	gr.shelves = map[goodreads.ShelfName][]goodreads.ShelfBook{
		goodreads.ShelfWantToRead:       {{GoodreadsBookID: "1", Title: "Dune", Author: "Frank Herbert"}},
		goodreads.ShelfCurrentlyReading: {{GoodreadsBookID: "2", Title: "Dune Messiah", Author: "Frank Herbert"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/pending/"+strconv.FormatInt(pm.ID, 10)+"?q=Dune", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Dune Messiah") {
		t.Errorf("expected search results from both shelves, got:\n%s", body)
	}
}
