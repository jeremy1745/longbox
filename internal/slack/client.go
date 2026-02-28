package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Message represents a Slack incoming webhook payload.
type Message struct {
	Text string `json:"text"`
}

// Client sends messages to a Slack incoming webhook.
type Client struct {
	webhookURL string
	httpClient *http.Client
}

// NewClient creates a Slack webhook client.
func NewClient(webhookURL string) *Client {
	return &Client{
		webhookURL: webhookURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Send posts a message to the configured Slack webhook.
func (c *Client) Send(msg Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling slack message: %w", err)
	}

	resp, err := c.httpClient.Post(c.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("posting to slack: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned status %d", resp.StatusCode)
	}
	return nil
}

// TestWebhook sends a test message to verify the webhook is configured correctly.
func (c *Client) TestWebhook() error {
	return c.Send(Message{Text: "LongBox test notification — your Slack webhook is working!"})
}
