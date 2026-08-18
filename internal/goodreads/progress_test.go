package goodreads

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
)

func csrfPage(token string) string {
	return `<html><head><meta name="csrf-token" content="` + token + `" /></head><body></body></html>`
}

func TestUpdateProgressSuccess(t *testing.T) {
	var gotForm url.Values
	var gotCSRFHeader, gotXRequestedWith string

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user/show/999":
			io.WriteString(w, csrfPage("tok-1"))
		case r.URL.Path == "/user_status.json":
			body, _ := io.ReadAll(r.Body)
			gotForm, _ = url.ParseQuery(string(body))
			gotCSRFHeader = r.Header.Get("X-CSRF-Token")
			gotXRequestedWith = r.Header.Get("X-Requested-With")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"updatedAt":"2026-01-01T00:00:00Z","finalPosition":100,"currentPosition":85,"positionType":"percent"}`)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})

	if err := c.UpdateProgress(context.Background(), "40605629", 85); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	if gotForm.Get("user_status[book_id]") != "40605629" {
		t.Errorf("book_id = %q", gotForm.Get("user_status[book_id]"))
	}
	if gotForm.Get("user_status[percent]") != "85" {
		t.Errorf("percent = %q", gotForm.Get("user_status[percent]"))
	}
	if gotForm.Get("user_status[body]") != "" {
		t.Errorf("body = %q, want empty", gotForm.Get("user_status[body]"))
	}
	if gotCSRFHeader != "tok-1" {
		t.Errorf("X-CSRF-Token = %q", gotCSRFHeader)
	}
	if gotXRequestedWith != "XMLHttpRequest" {
		t.Errorf("X-Requested-With = %q", gotXRequestedWith)
	}
}

func TestUpdateProgressRetriesOnceOnStaleCSRFToken(t *testing.T) {
	var csrfFetches, posts int32

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user/show/999":
			n := atomic.AddInt32(&csrfFetches, 1)
			io.WriteString(w, csrfPage(map[int32]string{1: "tok-1", 2: "tok-2"}[n]))
		case r.URL.Path == "/user_status.json":
			n := atomic.AddInt32(&posts, 1)
			if n == 1 {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"updatedAt":"2026-01-01T00:00:00Z","finalPosition":50,"currentPosition":50,"positionType":"percent"}`)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})

	if err := c.UpdateProgress(context.Background(), "1", 50); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	if csrfFetches != 2 {
		t.Errorf("expected 2 csrf fetches (initial + after invalidate), got %d", csrfFetches)
	}
	if posts != 2 {
		t.Errorf("expected 2 post attempts, got %d", posts)
	}
}

func TestUpdateProgressPersistentCSRFRejectionBecomesSessionInvalid(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user/show/999":
			io.WriteString(w, csrfPage("tok"))
		case r.URL.Path == "/user_status.json":
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})

	err := c.UpdateProgress(context.Background(), "1", 50)
	if err != ErrSessionInvalid {
		t.Fatalf("expected ErrSessionInvalid, got %v", err)
	}
}

func TestUpdateProgressSignInRedirectIsImmediateSessionInvalid(t *testing.T) {
	var posts int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user/show/999":
			io.WriteString(w, csrfPage("tok"))
		case r.URL.Path == "/user_status.json":
			atomic.AddInt32(&posts, 1)
			http.Redirect(w, r, "/user/sign_in", http.StatusFound)
		case r.URL.Path == "/user/sign_in":
			io.WriteString(w, `sign in please`)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})

	err := c.UpdateProgress(context.Background(), "1", 50)
	if err != ErrSessionInvalid {
		t.Fatalf("expected ErrSessionInvalid, got %v", err)
	}
	if posts != 1 {
		t.Errorf("expected exactly 1 post attempt (no CSRF-style retry on an auth-shaped failure), got %d", posts)
	}
}
