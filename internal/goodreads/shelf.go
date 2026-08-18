package goodreads

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
)

// shelfRSS mirrors Goodreads' public review/list_rss feed shape, confirmed
// against github.com/mariannefeng/piratereads (an unofficial Goodreads
// wrapper that reads the same feed). No auth is needed for this endpoint —
// it's a public per-shelf RSS feed, which only works if the shelves are
// public.
type shelfRSS struct {
	Channel struct {
		Items []shelfRSSItem `xml:"item"`
	} `xml:"channel"`
}

type shelfRSSItem struct {
	Title      string `xml:"title"`
	Link       string `xml:"link"`
	AuthorName string `xml:"author_name"`
	BookID     string `xml:"book_id"`
}

// bookIDPattern extracts the numeric book ID from a Goodreads book URL,
// e.g. https://www.goodreads.com/book/show/40605629-project-hail-mary.
var bookIDPattern = regexp.MustCompile(`/book/show/(\d+)`)

// FetchShelf fetches all books on the given public shelf via Goodreads'
// RSS feed, GET /review/list_rss/{user_id}?shelf={shelf}&per_page=200.
// Needs no authentication.
func (c *Client) FetchShelf(ctx context.Context, shelf ShelfName) ([]ShelfBook, error) {
	path := fmt.Sprintf("/review/list_rss/%s?shelf=%s&per_page=200", c.userID, shelf)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("goodreads: fetch shelf %s: %w", shelf, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// A private/nonexistent shelf 404s (or otherwise errors) silently
		// from Goodreads' side rather than returning a helpful message.
		return nil, fmt.Errorf("goodreads: fetch shelf %s: status %d (is GOODREADS_USER_ID's shelf public?)", shelf, resp.StatusCode)
	}

	var rss shelfRSS
	if err := xml.NewDecoder(resp.Body).Decode(&rss); err != nil {
		return nil, fmt.Errorf("goodreads: decode shelf %s rss: %w", shelf, err)
	}

	books := make([]ShelfBook, 0, len(rss.Channel.Items))
	for _, item := range rss.Channel.Items {
		// The feed's own <book_id> is the real source of truth — <link>
		// points at /review/show/{review_id}, not a /book/show/{book_id}
		// URL (confirmed against a live feed; an earlier version of this
		// code assumed the latter and silently dropped every item as a
		// result). bookIDFromURL is kept only as a defensive fallback in
		// case a future feed shape ever omits <book_id>.
		id := item.BookID
		if id == "" {
			id = bookIDFromURL(item.Link)
		}
		if id == "" {
			// Skip rather than fail the whole shelf fetch over one
			// malformed entry — matching still works for the rest.
			continue
		}
		books = append(books, ShelfBook{
			GoodreadsBookID: id,
			Title:           item.Title,
			Author:          item.AuthorName,
		})
	}
	return books, nil
}

// bookIDFromURL extracts the numeric book ID from a Goodreads book URL, or
// "" if the URL doesn't match the expected shape.
func bookIDFromURL(u string) string {
	m := bookIDPattern.FindStringSubmatch(u)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}
