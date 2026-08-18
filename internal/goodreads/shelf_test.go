package goodreads

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	c := NewClient("999")
	c.baseURL = ts.URL
	return c, ts
}

func TestFetchShelfParsesRSSAndExtractsBookID(t *testing.T) {
	fixture, err := os.ReadFile("testdata/currently-reading.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Query().Get("shelf") != "currently-reading" {
			t.Errorf("expected shelf=currently-reading, got %q", r.URL.Query().Get("shelf"))
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write(fixture)
	})

	books, err := c.FetchShelf(context.Background(), ShelfCurrentlyReading)
	if err != nil {
		t.Fatalf("FetchShelf: %v", err)
	}

	if gotPath != "/review/list_rss/999" {
		t.Errorf("expected path /review/list_rss/999, got %q", gotPath)
	}

	// The fixture has 3 items but one has no parseable book ID and should
	// be skipped rather than failing the whole fetch.
	if len(books) != 2 {
		t.Fatalf("expected 2 books (malformed entry skipped), got %d: %+v", len(books), books)
	}

	if books[0].GoodreadsBookID != "54493401" || books[0].Title != "Project Hail Mary" || books[0].Author != "Andy Weir" {
		t.Errorf("unexpected first book: %+v", books[0])
	}
	if books[1].GoodreadsBookID != "19161852" {
		t.Errorf("unexpected second book id: %+v", books[1])
	}
}

func TestFetchShelfNonOKStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := c.FetchShelf(context.Background(), ShelfWantToRead)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestBookIDFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.goodreads.com/book/show/40605629-project-hail-mary": "40605629",
		"https://www.goodreads.com/book/show/1234567":                    "1234567",
		"https://www.goodreads.com/some/other/path":                      "",
		"": "",
	}
	for in, want := range cases {
		if got := bookIDFromURL(in); got != want {
			t.Errorf("bookIDFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
