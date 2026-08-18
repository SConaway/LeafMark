package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "leafmark.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return database
}

func TestMigrateIsIdempotent(t *testing.T) {
	database := openTestDB(t)
	if err := Migrate(database); err != nil {
		t.Fatalf("second Migrate call failed (schema is not idempotent): %v", err)
	}
	if err := Migrate(database); err != nil {
		t.Fatalf("third Migrate call failed: %v", err)
	}
}

// TestForeignKeysEnforcedPerConnection is the load-bearing test for the
// spec's explicitly-flagged pitfall: PRAGMA foreign_keys=ON is per-connection
// in SQLite, so it must be set via the driver's DSN, not a one-time raw
// PRAGMA statement after Open. If that regresses, both assertions below
// would silently pass (SQLite defaults to foreign_keys=OFF).
func TestForeignKeysEnforcedPerConnection(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	// Orphan insert into a child table must be rejected.
	_, err := database.ExecContext(ctx, `
		INSERT INTO sync_state (doc_hash, last_percent, last_sync_status)
		VALUES ('nonexistent-doc', 50, 'ok')
	`)
	if err == nil {
		t.Fatal("expected FK violation inserting sync_state for a nonexistent doc_hash, got nil error")
	}

	// Cascade delete must actually remove dependent rows.
	if err := UpsertDocument(ctx, database, "doc-1", "Project Hail Mary", "Andy Weir"); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	if err := UpsertSyncState(ctx, database, "doc-1", 42, SyncStatusOK, ""); err != nil {
		t.Fatalf("UpsertSyncState: %v", err)
	}
	if err := UpsertMapping(ctx, database, UpsertMappingParams{
		DocHash: "doc-1", GoodreadsBookID: "40605629", GoodreadsTitle: "Project Hail Mary", MatchedVia: MatchedViaAuto,
	}); err != nil {
		t.Fatalf("UpsertMapping: %v", err)
	}
	if _, err := CreatePendingMatch(ctx, database, "doc-1", 42, nil); err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}

	if _, err := database.ExecContext(ctx, `DELETE FROM documents WHERE doc_hash = ?`, "doc-1"); err != nil {
		t.Fatalf("delete documents: %v", err)
	}

	for table, query := range map[string]string{
		"sync_state":      `SELECT COUNT(*) FROM sync_state WHERE doc_hash = 'doc-1'`,
		"book_mappings":   `SELECT COUNT(*) FROM book_mappings WHERE doc_hash = 'doc-1'`,
		"pending_matches": `SELECT COUNT(*) FROM pending_matches WHERE doc_hash = 'doc-1'`,
	} {
		var count int
		if err := database.QueryRowContext(ctx, query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s: expected cascade delete to remove all rows, found %d", table, count)
		}
	}
}

func TestPendingMatchOnlyOneOpenPerDoc(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	if err := UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	if _, err := CreatePendingMatch(ctx, database, "doc-1", 10, nil); err != nil {
		t.Fatalf("first CreatePendingMatch: %v", err)
	}
	if _, err := CreatePendingMatch(ctx, database, "doc-1", 20, nil); err != ErrAlreadyPending {
		t.Fatalf("expected ErrAlreadyPending, got %v", err)
	}
}

func TestResolvePendingMatchIsIdempotent(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	if err := UpsertDocument(ctx, database, "doc-1", "Some Book", ""); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	pm, err := CreatePendingMatch(ctx, database, "doc-1", 10, []Candidate{
		{GoodreadsBookID: "1", Title: "Some Book", Score: 0.9},
	})
	if err != nil {
		t.Fatalf("CreatePendingMatch: %v", err)
	}

	resolved, err := ResolvePendingMatch(ctx, database, pm.ID)
	if err != nil {
		t.Fatalf("first ResolvePendingMatch: %v", err)
	}
	if !resolved {
		t.Fatal("expected first resolve to report resolved=true")
	}

	resolved, err = ResolvePendingMatch(ctx, database, pm.ID)
	if err != nil {
		t.Fatalf("second ResolvePendingMatch: %v", err)
	}
	if resolved {
		t.Fatal("expected second resolve (double-tap) to report resolved=false, not re-resolve")
	}

	got, err := GetPendingMatch(ctx, database, pm.ID)
	if err != nil {
		t.Fatalf("GetPendingMatch: %v", err)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].GoodreadsBookID != "1" {
		t.Fatalf("candidates round-trip mismatch: %+v", got.Candidates)
	}
}
