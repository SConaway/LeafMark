package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"leafmark/internal/db"
)

func post(s *Server, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	return rec
}

func seedPending(t *testing.T, database *sql.DB, hash string) *db.PendingMatch {
	t.Helper()
	ctx := context.Background()
	if err := db.UpsertDocument(ctx, database, hash, "Some PDF", ""); err != nil {
		t.Fatal(err)
	}
	pm, err := db.CreatePendingMatch(ctx, database, hash, 12, []db.Candidate{{GoodreadsBookID: "1", Title: "A", Score: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	return pm
}

func TestResendPublishesNotification(t *testing.T) {
	s, database, _, n := newTestServerWithNotifier(t)
	pm := seedPending(t, database, "doc-1")

	rec := post(s, "/pending/"+strconv.FormatInt(pm.ID, 10)+"/resend")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if n.count() != 1 {
		t.Fatalf("notifications = %d, want 1", n.count())
	}
}

func TestResendAllSkipsNonOpen(t *testing.T) {
	s, database, _, n := newTestServerWithNotifier(t)
	seedPending(t, database, "doc-1")
	pm2 := seedPending(t, database, "doc-2")
	if _, err := db.DismissPendingMatch(context.Background(), database, pm2.ID); err != nil {
		t.Fatal(err)
	}

	if rec := post(s, "/pending/resend-all"); rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if n.count() != 1 {
		t.Fatalf("notifications = %d, want 1", n.count())
	}
}

func TestIgnoreDismissesAndMarksDocument(t *testing.T) {
	s, database, _, _ := newTestServerWithNotifier(t)
	ctx := context.Background()
	pm := seedPending(t, database, "doc-1")

	if rec := post(s, "/pending/"+strconv.FormatInt(pm.ID, 10)+"/ignore"); rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	got, _ := db.GetPendingMatch(ctx, database, pm.ID)
	if got.Status != db.PendingStatusDismissed {
		t.Errorf("status = %s", got.Status)
	}
	if ok, _ := db.IsIgnored(ctx, database, "doc-1"); !ok {
		t.Error("doc not ignored")
	}

	if rec := post(s, "/ignored/doc-1/restore"); rec.Code != http.StatusSeeOther {
		t.Fatalf("restore status = %d", rec.Code)
	}
	if ok, _ := db.IsIgnored(ctx, database, "doc-1"); ok {
		t.Error("doc still ignored after restore")
	}
}
