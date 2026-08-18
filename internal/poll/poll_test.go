package poll

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"leafmark/internal/db"
	"leafmark/internal/goodreads"
	"leafmark/internal/koreader"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "leafmark.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return database
}

func newTestPoller(t *testing.T, database *sql.DB, kor koreader.KOReader, gr *mockGoodreads, notifier *mockNotifier) *Poller {
	t.Helper()
	return NewPoller(Deps{
		DB: database, KOReader: kor, Goodreads: gr, Notifier: notifier,
		MatchThreshold: 0.8, BaseURL: "https://leafmark.example.ts.net",
	})
}

func TestRunOnceNoDocumentsYetIsNoOp(t *testing.T) {
	database := openTestDB(t)
	p := newTestPoller(t, database, &mockKOReader{ok: false}, &mockGoodreads{}, &mockNotifier{})

	if err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

func TestRunOnceUnchangedProgressIsNoOp(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	if err := db.UpsertSyncState(ctx, database, "doc-1", 42, db.SyncStatusOK, ""); err != nil {
		t.Fatalf("UpsertSyncState: %v", err)
	}

	gr := &mockGoodreads{}
	kor := &mockKOReader{ok: true, doc: koreader.Document{DocHash: "doc-1", Title: "Some Book", Percent: 42}}
	p := newTestPoller(t, database, kor, gr, &mockNotifier{})

	if err := p.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if gr.callCount() != 0 {
		t.Errorf("expected no Goodreads calls for unchanged progress, got %d", gr.callCount())
	}
}

func TestRunOnceMappedDocPushesDirectly(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Project Hail Mary", "Andy Weir"); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	if err := db.UpsertMapping(ctx, database, db.UpsertMappingParams{
		DocHash: "doc-1", GoodreadsBookID: "40605629", GoodreadsTitle: "Project Hail Mary", MatchedVia: db.MatchedViaAuto,
	}); err != nil {
		t.Fatalf("UpsertMapping: %v", err)
	}

	gr := &mockGoodreads{}
	kor := &mockKOReader{ok: true, doc: koreader.Document{DocHash: "doc-1", Title: "Project Hail Mary", Author: "Andy Weir", Percent: 55}}
	p := newTestPoller(t, database, kor, gr, &mockNotifier{})

	if err := p.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if gr.callCount() != 1 || gr.lastCall() != (updateCall{"40605629", 55}) {
		t.Fatalf("unexpected calls: %+v", gr.calls)
	}

	state, err := db.GetSyncState(ctx, database, "doc-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if state.LastSyncStatus != db.SyncStatusOK || state.LastPercent != 55 {
		t.Errorf("unexpected sync state: %+v", state)
	}
}

func TestRunOnceUnmappedDocAboveThresholdAutoMatchesAndPushes(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	gr := &mockGoodreads{shelves: map[goodreads.ShelfName][]goodreads.ShelfBook{
		goodreads.ShelfCurrentlyReading: {{GoodreadsBookID: "40605629", Title: "Project Hail Mary", Author: "Andy Weir"}},
	}}
	kor := &mockKOReader{ok: true, doc: koreader.Document{DocHash: "doc-1", Title: "Project Hail Mary: A Novel", Author: "Andy Weir", Percent: 10}}
	notifier := &mockNotifier{}
	p := newTestPoller(t, database, kor, gr, notifier)

	if err := p.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	mapping, err := db.GetMapping(ctx, database, "doc-1")
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping.MatchedVia != db.MatchedViaAuto || mapping.GoodreadsBookID != "40605629" {
		t.Errorf("unexpected mapping: %+v", mapping)
	}
	if gr.callCount() != 1 {
		t.Errorf("expected a push after auto-match, got %d calls", gr.callCount())
	}
	if notifier.count() != 0 {
		t.Errorf("expected no ntfy notification for an auto-confirmed match, got %d", notifier.count())
	}
}

func TestRunOnceUnmappedDocBelowThresholdCreatesPendingAndNotifiesWithoutPushing(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	gr := &mockGoodreads{shelves: map[goodreads.ShelfName][]goodreads.ShelfBook{
		goodreads.ShelfWantToRead: {{GoodreadsBookID: "1", Title: "Completely Unrelated Book", Author: "Nobody"}},
	}}
	kor := &mockKOReader{ok: true, doc: koreader.Document{DocHash: "doc-1", Title: "Project Hail Mary", Author: "Andy Weir", Percent: 10}}
	notifier := &mockNotifier{}
	p := newTestPoller(t, database, kor, gr, notifier)

	if err := p.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if gr.callCount() != 0 {
		t.Errorf("expected no push while a match is pending, got %d calls", gr.callCount())
	}
	if _, err := db.GetMapping(ctx, database, "doc-1"); err != db.ErrNotFound {
		t.Errorf("expected no mapping yet, got err=%v", err)
	}

	pm, err := db.GetOpenPendingMatchByDocHash(ctx, database, "doc-1")
	if err != nil {
		t.Fatalf("GetOpenPendingMatchByDocHash: %v", err)
	}
	if pm.ProgressPct != 10 {
		t.Errorf("progress_pct = %v", pm.ProgressPct)
	}

	if notifier.count() != 1 {
		t.Fatalf("expected exactly 1 ntfy notification, got %d", notifier.count())
	}
	n := notifier.last()
	if len(n.Actions) == 0 || n.Actions[len(n.Actions)-1].Action != "view" {
		t.Errorf("expected a trailing view action, got %+v", n.Actions)
	}
}

func TestRunOnceSecondPollDoesNotDuplicatePendingOrNotification(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	gr := &mockGoodreads{shelves: map[goodreads.ShelfName][]goodreads.ShelfBook{
		goodreads.ShelfWantToRead: {{GoodreadsBookID: "1", Title: "Unrelated", Author: "Nobody"}},
	}}
	kor := &mockKOReader{ok: true, doc: koreader.Document{DocHash: "doc-1", Title: "Project Hail Mary", Author: "Andy Weir", Percent: 10}}
	notifier := &mockNotifier{}
	p := newTestPoller(t, database, kor, gr, notifier)

	if err := p.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	// Progress unchanged and status is still "no sync_state row" (never
	// pushed), so a naive implementation might re-run the match step.
	if err := p.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}

	if notifier.count() != 1 {
		t.Errorf("expected the second poll not to re-notify, got %d notifications", notifier.count())
	}
}

func TestRunOnceGoodreadsErrorIsRecordedNotFatal(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	if err := db.UpsertMapping(ctx, database, db.UpsertMappingParams{
		DocHash: "doc-1", GoodreadsBookID: "1", GoodreadsTitle: "Some Book", MatchedVia: db.MatchedViaAuto,
	}); err != nil {
		t.Fatalf("UpsertMapping: %v", err)
	}

	gr := &mockGoodreads{updateErrs: []error{errBoom}}
	kor := &mockKOReader{ok: true, doc: koreader.Document{DocHash: "doc-1", Title: "Some Book", Percent: 33}}
	p := newTestPoller(t, database, kor, gr, &mockNotifier{})

	if err := p.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce should not fail the whole cycle on a push error: %v", err)
	}

	state, err := db.GetSyncState(ctx, database, "doc-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if state.LastSyncStatus != db.SyncStatusError {
		t.Errorf("expected sync_state status=error, got %q", state.LastSyncStatus)
	}
}

func TestRunOnceSessionInvalidTriggersReloginAndRetriesOnce(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	if err := db.UpsertMapping(ctx, database, db.UpsertMappingParams{
		DocHash: "doc-1", GoodreadsBookID: "1", GoodreadsTitle: "Some Book", MatchedVia: db.MatchedViaAuto,
	}); err != nil {
		t.Fatalf("UpsertMapping: %v", err)
	}

	gr := &mockGoodreads{updateErrs: []error{goodreads.ErrSessionInvalid, nil}}
	kor := &mockKOReader{ok: true, doc: koreader.Document{DocHash: "doc-1", Title: "Some Book", Percent: 60}}
	reloginCalls := 0
	p := newTestPoller(t, database, kor, gr, &mockNotifier{})
	p.Relogin = func(ctx context.Context) error {
		reloginCalls++
		return nil
	}

	if err := p.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if reloginCalls != 1 {
		t.Errorf("expected exactly 1 relogin call, got %d", reloginCalls)
	}
	if gr.callCount() != 2 {
		t.Errorf("expected an initial failed call + 1 retry, got %d calls", gr.callCount())
	}

	state, err := db.GetSyncState(ctx, database, "doc-1")
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	if state.LastSyncStatus != db.SyncStatusOK {
		t.Errorf("expected the retry to succeed, got status %q (%s)", state.LastSyncStatus, state.LastError.String)
	}
}

func TestRunOnceSessionInvalidAlertIsThrottledToOnce(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	if err := db.UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	if err := db.UpsertMapping(ctx, database, db.UpsertMappingParams{
		DocHash: "doc-1", GoodreadsBookID: "1", GoodreadsTitle: "Some Book", MatchedVia: db.MatchedViaAuto,
	}); err != nil {
		t.Fatalf("UpsertMapping: %v", err)
	}

	gr := &mockGoodreads{updateErrs: []error{goodreads.ErrSessionInvalid}}
	notifier := &mockNotifier{}
	p := newTestPoller(t, database, &mockKOReader{}, gr, notifier)
	p.Relogin = func(ctx context.Context) error { return errRelogin }

	percent := 10
	for i := 0; i < 3; i++ {
		percent++
		p.KOReader = &mockKOReader{ok: true, doc: koreader.Document{DocHash: "doc-1", Title: "Some Book", Percent: percent}}
		if err := p.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce iteration %d: %v", i, err)
		}
	}

	if notifier.count() != 1 {
		t.Errorf("expected exactly 1 throttled alert across 3 consecutive failures, got %d", notifier.count())
	}
}

var errBoom = &testError{"boom"}

type testError struct{ s string }

func (e *testError) Error() string { return e.s }
