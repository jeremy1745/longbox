package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Message represents a Slack chat.postMessage payload.
type Message struct {
	Text string `json:"text"`
}

// Client sends messages via the Slack Web API.
type Client struct {
	token      string
	channel    string
	httpClient *http.Client
}

// NewClient creates a Slack Web API client.
func NewClient(token, channel string) *Client {
	return &Client{
		token:      token,
		channel:    channel,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// slackResponse is the envelope returned by Slack Web API methods.
type slackResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Send posts a message to the configured Slack channel via chat.postMessage.
func (c *Client) Send(msg Message) error {
	payload := struct {
		Channel string `json:"channel"`
		Text    string `json:"text"`
	}{
		Channel: c.channel,
		Text:    msg.Text,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling slack message: %w", err)
	}

	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting to slack: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned HTTP %d", resp.StatusCode)
	}

	var sr slackResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return fmt.Errorf("decoding slack response: %w", err)
	}
	if !sr.OK {
		return fmt.Errorf("slack API error: %s", sr.Error)
	}

	return nil
}

// Test sends a test message to verify the bot token and channel are configured correctly.
func (c *Client) Test() error {
	return c.Send(Message{Text: "LongBox test notification — your Slack integration is working!"})
}
