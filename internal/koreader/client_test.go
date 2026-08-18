package koreader

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLastSyncedDocumentReturnsMostRecentFirst(t *testing.T) {
	var gotUser, gotKey string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.Header.Get("X-Auth-User")
		gotKey = r.Header.Get("X-Auth-Key")
		if r.URL.Path != "/syncs/documents" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"documents":[
			{"document":"doc-hash-2","progress":"/body/DocFragment[10]","percentage":0.85,"device":"kobo","device_id":"abc","filename":"book2.epub","title":"Project Hail Mary","authors":"Andy Weir","timestamp":2000},
			{"document":"doc-hash-1","progress":"/body/DocFragment[1]","percentage":0.12,"device":"kobo","device_id":"abc","filename":"book1.epub","title":"Older Book","authors":"Someone","timestamp":1000}
		]}`)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "koreader-user", "koreader-pass")
	doc, ok, err := c.LastSyncedDocument(context.Background())
	if err != nil {
		t.Fatalf("LastSyncedDocument: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if doc.DocHash != "doc-hash-2" {
		t.Errorf("expected the first (most recent) document, got %+v", doc)
	}
	if doc.Percent != 85 {
		t.Errorf("expected 0.85 fraction to convert to 85 percent, got %d", doc.Percent)
	}
	if gotUser != "koreader-user" || gotKey != "koreader-pass" {
		t.Errorf("auth headers = %q/%q", gotUser, gotKey)
	}
}

func TestLastSyncedDocumentNoDocuments(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"documents":[]}`)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "p")
	_, ok, err := c.LastSyncedDocument(context.Background())
	if err != nil {
		t.Fatalf("LastSyncedDocument: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a user with no synced documents")
	}
}

func TestLastSyncedDocumentAuthFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "wrong", "creds")
	_, _, err := c.LastSyncedDocument(context.Background())
	if err == nil {
		t.Fatal("expected an error on 401")
	}
}

func TestFractionToPercentRounds(t *testing.T) {
	cases := map[float64]int{0.0: 0, 0.5: 50, 0.851: 85, 0.855: 86, 1.0: 100}
	for in, want := range cases {
		if got := fractionToPercent(in); got != want {
			t.Errorf("fractionToPercent(%v) = %d, want %d", in, got, want)
		}
	}
}
