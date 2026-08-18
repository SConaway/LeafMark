package ntfy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientParsesTopicURL(t *testing.T) {
	c, err := NewClient("https://ntfy.example.com/my-topic")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.serverRoot != "https://ntfy.example.com" {
		t.Errorf("serverRoot = %q", c.serverRoot)
	}
	if c.topic != "my-topic" {
		t.Errorf("topic = %q", c.topic)
	}
}

func TestNewClientRejectsMissingTopic(t *testing.T) {
	if _, err := NewClient("https://ntfy.example.com/"); err == nil {
		t.Fatal("expected error for a URL with no topic path")
	}
}

func TestPublishSendsExpectedJSON(t *testing.T) {
	var gotContentType string
	var gotBody publishBody

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c, err := NewClient(ts.URL + "/leafmark-topic")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	n := Notification{
		Title:   "Match needed: Project Hail Mary",
		Message: "Currently at 42%",
		Actions: []Action{
			{Action: "http", Label: "Project Hail Mary", URL: "https://leafmark.example.ts.net/confirm", Method: http.MethodPost, Clear: true},
			{Action: "http", Label: "Project Hail Mary (Signed)", URL: "https://leafmark.example.ts.net/confirm", Method: http.MethodPost, Clear: true},
			{Action: "view", Label: "Choose manually", URL: "https://leafmark.example.ts.net/pending/1"},
		},
	}

	if err := c.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if gotBody.Topic != "leafmark-topic" {
		t.Errorf("topic = %q", gotBody.Topic)
	}
	if gotBody.Title != n.Title || gotBody.Message != n.Message {
		t.Errorf("title/message mismatch: %+v", gotBody)
	}
	if len(gotBody.Actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(gotBody.Actions))
	}
	if gotBody.Actions[0].Action != "http" || !gotBody.Actions[0].Clear {
		t.Errorf("unexpected first action: %+v", gotBody.Actions[0])
	}
	if gotBody.Actions[2].Action != "view" {
		t.Errorf("unexpected last action: %+v", gotBody.Actions[2])
	}
}

func TestPublishNonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	c, err := NewClient(ts.URL + "/topic")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if err := c.Publish(context.Background(), Notification{Message: "hi"}); err == nil {
		t.Fatal("expected error on non-200 status")
	}
}
