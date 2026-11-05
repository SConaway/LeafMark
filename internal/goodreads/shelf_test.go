package goodreads

import (
	"context"
	"errors"
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

func TestFetchShelfParsesHTMLAndPaginates(t *testing.T) {
	fixture, err := os.ReadFile("testdata/shelf-page.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var pages []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/review/list/999" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.URL.Query().Get("shelf") != "currently-reading" {
			t.Errorf("expected shelf=currently-reading, got %q", r.URL.Query().Get("shelf"))
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if page == "2" {
			// Last page: no next link, no rows.
			w.Write([]byte("<html><body><table id=\"books\"></table></body></html>"))
			return
		}
		w.Write(fixture)
	})

	books, err := c.FetchShelf(context.Background(), ShelfCurrentlyReading)
	if err != nil {
		t.Fatalf("FetchShelf: %v", err)
	}
	if len(pages) != 2 {
		t.Errorf("expected 2 page requests, got %v", pages)
	}
	if len(books) != 3 {
		t.Fatalf("expected 3 books, got %d: %+v", len(books), books)
	}
	want := ShelfBook{GoodreadsBookID: "15839976", Title: "Red Rising", Author: "Pierce Brown"}
	if books[0] != want {
		t.Errorf("first book = %+v, want %+v", books[0], want)
	}
	if books[2].Author != "Dennis E. Taylor" || books[2].GoodreadsBookID != "32109569" {
		t.Errorf("unexpected third book: %+v", books[2])
	}
}

func TestFetchShelfSignInRedirectIsSessionInvalid(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/sign_in" {
			w.Write([]byte("sign in"))
			return
		}
		http.Redirect(w, r, "/user/sign_in", http.StatusFound)
	})
	_, err := c.FetchShelf(context.Background(), ShelfWantToRead)
	if !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expected ErrSessionInvalid, got %v", err)
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

func TestFlipAuthor(t *testing.T) {
	cases := map[string]string{
		"Brown, Pierce": "Pierce Brown",
		"Cher":          "Cher",
		"  Weir,  Andy": "Andy Weir",
	}
	for in, want := range cases {
		if got := flipAuthor(in); got != want {
			t.Errorf("flipAuthor(%q) = %q, want %q", in, got, want)
		}
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
