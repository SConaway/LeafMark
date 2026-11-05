package goodreads

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// bookIDPattern extracts the numeric book ID from a Goodreads book URL,
// e.g. /book/show/40605629-project-hail-mary.
var bookIDPattern = regexp.MustCompile(`/book/show/(\d+)`)

// maxShelfPages bounds pagination so a misbehaving "next" link can't loop
// forever.
const maxShelfPages = 50

// FetchShelf fetches all books on the given shelf by scraping the logged-in
// HTML shelf page, GET /review/list/{user_id}?shelf={shelf}&per_page=100,
// following pagination. (Goodreads removed the public review/list_rss feed
// this used to read.) Requires a valid session cookie; a redirect to
// sign-in is reported as ErrSessionInvalid so callers can re-login.
func (c *Client) FetchShelf(ctx context.Context, shelf ShelfName) ([]ShelfBook, error) {
	var books []ShelfBook
	for page := 1; page <= maxShelfPages; page++ {
		path := fmt.Sprintf("/review/list/%s?shelf=%s&per_page=100&page=%d", c.userID, url.QueryEscape(string(shelf)), page)
		start := time.Now()
		pageBooks, hasNext, err := c.fetchShelfPage(ctx, shelf, path)
		if err == nil {
			log.Printf("goodreads: shelf %s page %d: %d book(s) in %s", shelf, page, len(pageBooks), time.Since(start).Round(time.Millisecond))
		}
		if err != nil {
			return nil, err
		}
		books = append(books, pageBooks...)
		if !hasNext || len(pageBooks) == 0 {
			break
		}
	}
	return books, nil
}

func (c *Client) fetchShelfPage(ctx context.Context, shelf ShelfName, path string) ([]ShelfBook, bool, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("goodreads: fetch shelf %s: %w", shelf, err)
	}
	defer resp.Body.Close()

	if looksLikeSignInRedirect(resp) {
		return nil, false, ErrSessionInvalid
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("goodreads: fetch shelf %s: status %d", shelf, resp.StatusCode)
	}

	books, hasNext, err := parseShelfHTML(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("goodreads: parse shelf %s html: %w", shelf, err)
	}
	return books, hasNext, nil
}

// parseShelfHTML extracts books from one page of the /review/list table
// and reports whether a "next page" link exists.
func parseShelfHTML(r io.Reader) ([]ShelfBook, bool, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, false, err
	}
	var books []ShelfBook
	hasNext := false
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch {
			case n.Data == "tr" && hasClass(n, "review"):
				if b, ok := parseShelfRow(n); ok {
					books = append(books, b)
				}
				return
			case n.Data == "a" && hasClass(n, "next_page"):
				hasNext = true
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)
	return books, hasNext, nil
}

// parseShelfRow reads one <tr class="review"> row. Rows with no
// recognisable book link are skipped rather than failing the whole fetch.
func parseShelfRow(tr *html.Node) (ShelfBook, bool) {
	titleTD := findNode(tr, func(n *html.Node) bool { return n.Data == "td" && hasClass(n, "title") })
	authorTD := findNode(tr, func(n *html.Node) bool { return n.Data == "td" && hasClass(n, "author") })
	if titleTD == nil {
		return ShelfBook{}, false
	}
	link := findNode(titleTD, func(n *html.Node) bool { return n.Data == "a" && attr(n, "href") != "" })
	if link == nil {
		return ShelfBook{}, false
	}
	id := bookIDFromURL(attr(link, "href"))
	if id == "" {
		return ShelfBook{}, false
	}

	// Title: the link's own text, excluding the <span> that holds the
	// series suffix, e.g. "Red Rising" not "Red Rising (Red Rising Saga, #1)".
	var title strings.Builder
	for ch := link.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.TextNode {
			title.WriteString(ch.Data)
		}
	}

	author := ""
	if authorTD != nil {
		if a := findNode(authorTD, func(n *html.Node) bool { return n.Data == "a" }); a != nil {
			author = flipAuthor(nodeText(a))
		}
	}
	return ShelfBook{
		GoodreadsBookID: id,
		Title:           strings.Join(strings.Fields(title.String()), " "),
		Author:          author,
	}, true
}

// flipAuthor turns the shelf table's "Last, First" into "First Last".
func flipAuthor(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if last, first, ok := strings.Cut(s, ", "); ok && !strings.Contains(first, ",") {
		return first + " " + last
	}
	return s
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func findNode(n *html.Node, pred func(*html.Node) bool) *html.Node {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.ElementNode && pred(ch) {
			return ch
		}
		if f := findNode(ch, pred); f != nil {
			return f
		}
	}
	return nil
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return b.String()
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
