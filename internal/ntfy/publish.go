// Package ntfy publishes human-in-the-loop match notifications to an ntfy
// (https://ntfy.sh) topic.
package ntfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Action is one ntfy notification action button.
type Action struct {
	Action  string            `json:"action"` // "http" or "view"
	Label   string            `json:"label"`
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Clear   bool              `json:"clear,omitempty"`
}

// Notification is what gets published for a pending match.
type Notification struct {
	Title   string
	Message string
	Actions []Action
}

// publishBody mirrors ntfy's JSON publish schema
// (https://docs.ntfy.sh/publish/#publish-as-json). Built as JSON rather
// than the header-based Actions: syntax since JSON handles quoting/escaping
// of dynamic titles and URLs far more reliably than a hand-built header
// string.
type publishBody struct {
	Topic   string   `json:"topic"`
	Title   string   `json:"title,omitempty"`
	Message string   `json:"message,omitempty"`
	Actions []Action `json:"actions,omitempty"`
}

// Publisher is the interface the rest of LeafMark depends on.
type Publisher interface {
	Publish(ctx context.Context, n Notification) error
}

// Client publishes to a single ntfy topic, given as a full topic URL
// (NTFY_URL), e.g. https://ntfy.sh/my-leafmark-topic.
type Client struct {
	serverRoot string
	topic      string
	httpClient *http.Client
}

// NewClient parses a full ntfy topic URL into the server root + topic
// Publish needs for the JSON publish endpoint.
func NewClient(topicURL string) (*Client, error) {
	u, err := url.Parse(topicURL)
	if err != nil {
		return nil, fmt.Errorf("ntfy: invalid NTFY_URL %q: %w", topicURL, err)
	}
	topic := strings.Trim(u.Path, "/")
	if topic == "" {
		return nil, fmt.Errorf("ntfy: NTFY_URL %q has no topic path", topicURL)
	}
	u.Path = ""
	return &Client{
		serverRoot: u.String(),
		topic:      topic,
		httpClient: &http.Client{},
	}, nil
}

// Publish sends the notification to the configured topic.
func (c *Client) Publish(ctx context.Context, n Notification) error {
	body := publishBody{
		Topic:   c.topic,
		Title:   n.Title,
		Message: n.Message,
		Actions: n.Actions,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("ntfy: marshal publish body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverRoot, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: publish: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ntfy: publish: unexpected status %d", resp.StatusCode)
	}
	return nil
}
