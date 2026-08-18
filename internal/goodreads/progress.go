package goodreads

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// progressResponse mirrors the JSON Goodreads returns from
// POST /user_status.json, confirmed via a captured HAR:
//
//	{"updatedAt":"...","finalPosition":100,"currentPosition":85,"positionType":"percent"}
type progressResponse struct {
	UpdatedAt       string `json:"updatedAt"`
	FinalPosition   int    `json:"finalPosition"`
	CurrentPosition int    `json:"currentPosition"`
	PositionType    string `json:"positionType"`
}

// UpdateProgress pushes a reading-progress percentage (0-100) for a book
// already on one of the user's shelves. Confirmed contract:
//
//	POST /user_status.json
//	Content-Type: application/x-www-form-urlencoded
//	X-CSRF-Token: <scraped token>
//	X-Requested-With: XMLHttpRequest
//	Cookie: <full raw cookie header>
//	body: user_status[book_id]=...&user_status[body]=&user_status[percent]=...
//
// On a CSRF-shaped failure the cached token is invalidated and the request
// retried once. On an auth-shaped failure (redirect to sign-in),
// ErrSessionInvalid is returned immediately with no retry — the caller
// (the poll loop) is responsible for triggering re-login.
func (c *Client) UpdateProgress(ctx context.Context, bookID string, percent int) error {
	log.Printf("goodreads: UpdateProgress(book_id=%s, percent=%d)", bookID, percent)

	token, err := c.ensureCSRFToken(ctx)
	if err != nil {
		log.Printf("goodreads: UpdateProgress: could not obtain a CSRF token: %v", err)
		return err
	}

	err = c.postProgress(ctx, bookID, percent, token)
	if err == errCSRFRejected {
		log.Print("goodreads: POST /user_status.json rejected the CSRF token (422) — invalidating and retrying once with a fresh one")
		c.invalidateCSRFToken()
		token, err = c.ensureCSRFToken(ctx)
		if err != nil {
			log.Printf("goodreads: UpdateProgress: could not obtain a fresh CSRF token for retry: %v", err)
			return err
		}
		err = c.postProgress(ctx, bookID, percent, token)
	}
	if err == errCSRFRejected {
		// Retried once with a fresh token and it's still being rejected —
		// don't loop forever on what's likely a real auth problem wearing
		// a CSRF-shaped mask.
		log.Print("goodreads: CSRF token still rejected after refetch+retry — treating as a real session failure, not a stale-token blip")
		return ErrSessionInvalid
	}
	if err != nil {
		log.Printf("goodreads: UpdateProgress(book_id=%s) failed: %v", bookID, err)
	} else {
		log.Printf("goodreads: UpdateProgress(book_id=%s, percent=%d) succeeded", bookID, percent)
	}
	return err
}

func (c *Client) postProgress(ctx context.Context, bookID string, percent int, csrfToken string) error {
	form := url.Values{}
	form.Set("user_status[book_id]", bookID)
	form.Set("user_status[body]", "")
	form.Set("user_status[percent]", strconv.Itoa(percent))

	req, err := c.newRequest(ctx, http.MethodPost, "/user_status.json", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	log.Printf("goodreads: POST %s/user_status.json (book_id=%s, percent=%d, csrf_token=%d bytes)", c.baseURL, bookID, percent, len(csrfToken))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("goodreads: POST /user_status.json request failed: %v", err)
		return fmt.Errorf("goodreads: update progress: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("goodreads: POST /user_status.json -> HTTP %d", resp.StatusCode)

	if looksLikeSignInRedirect(resp) {
		return ErrSessionInvalid
	}
	if resp.StatusCode == http.StatusUnprocessableEntity {
		return errCSRFRejected
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Printf("goodreads: POST /user_status.json unexpected status %d, body: %s", resp.StatusCode, body)
		return fmt.Errorf("goodreads: update progress: unexpected status %d: %s", resp.StatusCode, body)
	}

	var out progressResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("goodreads: decode update progress response: %w", err)
	}
	return nil
}
