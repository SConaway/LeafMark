package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"leafmark/internal/db"
)

func newTestServer(t *testing.T) (*Server, *sql.DB, *mockGoodreads) {
	t.Helper()
	database := openTestDB(t)
	gr := &mockGoodreads{}
	s, err := NewServer(database, gr, "https://leafmark.example.ts.net")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s, database, gr
}

func TestConfirmJSONResolvesAndPushes(t *testing.T) {
	s, database, gr := newTestServer(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Project Hail Mary", "Andy Weir"); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	pm, err := db.CreatePendingMatch(ctx, database, "doc-1", 42, nil)
	if err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}

	body := `{"pending_match_id":` + strconv.FormatInt(pm.ID, 10) + `,"goodreads_book_id":"40605629","goodreads_title":"Project Hail Mary","goodreads_author":"Andy Weir","source":"ntfy_action"}`
	req := httptest.NewRequest(http.MethodPost, "/confirm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("response = %+v", resp)
	}

	mapping, err := db.GetMapping(ctx, database, "doc-1")
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping.GoodreadsBookID != "40605629" || mapping.MatchedVia != db.MatchedViaNtfyAction {
		t.Errorf("unexpected mapping: %+v", mapping)
	}

	calls := gr.calls()
	if len(calls) != 1 || calls[0].BookID != "40605629" || calls[0].Percent != 42 {
		t.Fatalf("unexpected UpdateProgress calls: %+v", calls)
	}

	state, err := db.GetSyncState(ctx, database, "doc-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if state.LastSyncStatus != db.SyncStatusOK {
		t.Errorf("sync status = %q", state.LastSyncStatus)
	}
}

func TestConfirmDoubleTapIsNoOp(t *testing.T) {
	s, database, gr := newTestServer(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	pm, err := db.CreatePendingMatch(ctx, database, "doc-1", 10, nil)
	if err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}

	body := `{"pending_match_id":` + strconv.FormatInt(pm.ID, 10) + `,"goodreads_book_id":"1"}`

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/confirm", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d, body = %s", i, rec.Code, rec.Body.String())
		}
	}

	// Only the first tap should have actually pushed to Goodreads.
	if calls := gr.calls(); len(calls) != 1 {
		t.Fatalf("expected exactly 1 UpdateProgress call across both taps, got %d: %+v", len(calls), calls)
	}
}

func TestConfirmUnknownIDIsNoOp(t *testing.T) {
	s, _, gr := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/confirm", strings.NewReader(`{"pending_match_id":99999,"goodreads_book_id":"1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if calls := gr.calls(); len(calls) != 0 {
		t.Fatalf("expected no UpdateProgress calls for an unknown id, got %+v", calls)
	}
}

func TestConfirmFormSubmissionSetsWebUIMatchedViaAndRedirects(t *testing.T) {
	s, database, _ := newTestServer(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	pm, err := db.CreatePendingMatch(ctx, database, "doc-1", 10, nil)
	if err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}

	form := url.Values{}
	form.Set("pending_match_id", strconv.FormatInt(pm.ID, 10))
	form.Set("goodreads_book_id", "555")
	form.Set("source", "webui")

	req := httptest.NewRequest(http.MethodPost, "/confirm", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected a redirect for a form submission, got status %d body %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/pending" {
		t.Errorf("Location = %q", loc)
	}

	mapping, err := db.GetMapping(ctx, database, "doc-1")
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping.MatchedVia != db.MatchedViaWebUI {
		t.Errorf("matched_via = %q, want webui", mapping.MatchedVia)
	}
	if mapping.GoodreadsTitle != "Goodreads book 555" {
		t.Errorf("expected a default title when none was provided, got %q", mapping.GoodreadsTitle)
	}
}

func TestConfirmDismissedPendingMatchIsNoOp(t *testing.T) {
	s, database, gr := newTestServer(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	pm, err := db.CreatePendingMatch(ctx, database, "doc-1", 10, nil)
	if err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE pending_matches SET status = 'dismissed' WHERE id = ?`, pm.ID); err != nil {
		t.Fatalf("dismiss pending match: %v", err)
	}

	body := `{"pending_match_id":` + strconv.FormatInt(pm.ID, 10) + `,"goodreads_book_id":"1"}`
	req := httptest.NewRequest(http.MethodPost, "/confirm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if calls := gr.calls(); len(calls) != 0 {
		t.Fatalf("expected no push for a dismissed match, got %+v", calls)
	}
}
