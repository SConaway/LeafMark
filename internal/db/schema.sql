-- LeafMark schema (SQLite)
-- Tracks book identity mapping, sync state, and pending human-in-the-loop matches.

PRAGMA foreign_keys = ON;

-- One row per KOReader "document" we've ever seen, keyed by whatever stable
-- identifier the sync server gives us for a document (its partial MD5/binary
-- hash, per the KOReader Progress-sync protocol). This is the join key
-- between "what KOReader is telling us" and "what Goodreads book this is."
CREATE TABLE IF NOT EXISTS documents (
    doc_hash        TEXT PRIMARY KEY,   -- KOReader's document hash (from the sync API)
    koreader_title  TEXT NOT NULL,      -- last-seen title as reported by KOReader
    koreader_author TEXT,               -- last-seen author as reported by KOReader (nullable, often messy)
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Confirmed mapping from a KOReader document to a Goodreads book.
-- Once this exists, future progress updates for doc_hash are fully automatic.
CREATE TABLE IF NOT EXISTS book_mappings (
    doc_hash          TEXT PRIMARY KEY REFERENCES documents(doc_hash) ON DELETE CASCADE,
    goodreads_book_id TEXT NOT NULL,
    goodreads_title   TEXT NOT NULL,     -- cached, for display in logs/WebUI without re-fetching
    goodreads_author  TEXT,
    match_confidence  REAL,              -- fuzzy score at time of match, if auto; NULL if manual
    matched_via       TEXT NOT NULL CHECK (matched_via IN ('auto', 'ntfy_action', 'webui')),
    confirmed_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Last progress we successfully pushed to Goodreads for a mapped book.
-- Lets us diff incoming KOReader progress against "did this actually change"
-- so we don't spam Goodreads (or our own logs) on every poll.
CREATE TABLE IF NOT EXISTS sync_state (
    doc_hash           TEXT PRIMARY KEY REFERENCES documents(doc_hash) ON DELETE CASCADE,
    last_percent       REAL NOT NULL,
    last_synced_at     TEXT NOT NULL DEFAULT (datetime('now')),
    last_sync_status   TEXT NOT NULL CHECK (last_sync_status IN ('ok', 'error')),
    last_error         TEXT              -- populated when last_sync_status = 'error'
);

-- A document we saw with no confident match, awaiting human input via
-- an ntfy action button or the WebUI. One open row per doc_hash at a time.
CREATE TABLE IF NOT EXISTS pending_matches (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    doc_hash        TEXT NOT NULL REFERENCES documents(doc_hash) ON DELETE CASCADE,
    progress_pct    REAL NOT NULL,        -- the progress update that's waiting on a match
    candidates_json TEXT,                 -- top N fuzzy near-misses at notify time: [{book_id,title,author,score}, ...]
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_at     TEXT
);

-- Only one OPEN pending match per doc_hash at a time — enforced via a partial
-- unique index rather than a table-level UNIQUE, since resolved/dismissed rows
-- must be allowed to accumulate freely (e.g. re-matching after a long gap).
DROP INDEX IF EXISTS idx_pending_one_open_per_doc;
CREATE UNIQUE INDEX idx_pending_one_open_per_doc
    ON pending_matches (doc_hash)
    WHERE status = 'open';

CREATE INDEX IF NOT EXISTS idx_pending_status ON pending_matches (status);
CREATE INDEX IF NOT EXISTS idx_sync_state_last_synced ON sync_state (last_synced_at);
