// Package relais wraps the relais.intrane.fr inbox API for HITL decisions.
package relais

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Inbox is a relais catch-all URL with its owner token.
type Inbox struct {
	InboxID  string `json:"inbox_id"`
	Token    string `json:"token"`
	CatchURL string `json:"catch_url"`
	Plan     string `json:"plan,omitempty"`
}

// Message is one captured HTTP request.
type Message struct {
	ID         string `json:"id"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Headers    string `json:"headers"`
	Body       string `json:"body"`
	IP         string `json:"ip"`
	ReceivedAt string `json:"received_at"`
}

// Client talks to a relais relay.
type Client struct {
	BaseURL string
	Wallet  string
	HTTP    *http.Client
}

// NewClient returns a client configured from the environment.
func NewClient() *Client {
	base := os.Getenv("RELAIS_URL")
	if base == "" {
		base = "https://relais.intrane.fr"
	}
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		Wallet:  os.Getenv("RELAIS_PEAGE_WALLET"),
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{},
			},
		},
	}
}

// NewInbox creates a new relais inbox.
func (c *Client) NewInbox(label string) (*Inbox, error) {
	body, _ := json.Marshal(map[string]string{"label": label})
	req, err := http.NewRequest("POST", c.BaseURL+"/v1/inboxes", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Wallet != "" {
		req.Header.Set("X-Peage-Wallet", c.Wallet)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("relais request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("relais create inbox %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var out map[string]interface{}
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if !isOK(out["ok"]) {
		return nil, fmt.Errorf("relais create inbox returned ok=false")
	}

	in := &Inbox{
		InboxID:  jstr(out, "inbox_id"),
		Token:    jstr(out, "token"),
		CatchURL: jstr(out, "catch_url"),
		Plan:     jstr(out, "plan"),
	}
	if in.InboxID == "" || in.Token == "" || in.CatchURL == "" {
		return nil, fmt.Errorf("relais create inbox did not return inbox_id/token/catch_url")
	}
	return in, nil
}

// Messages returns the backlog for an inbox.
func (c *Client) Messages(token string) ([]Message, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/v1/messages", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("relais request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("relais messages %d", resp.StatusCode)
	}

	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var out map[string]interface{}
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if !isOK(out["ok"]) {
		return nil, fmt.Errorf("relais messages returned ok=false")
	}

	var msgs []Message
	if raw, ok := out["messages"].([]interface{}); ok {
		for _, item := range raw {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			msgs = append(msgs, Message{
				ID:         jstr(m, "id"),
				Method:     jstr(m, "method"),
				Path:       jstr(m, "path"),
				Headers:    jstr(m, "headers"),
				Body:       jstr(m, "body"),
				IP:         jstr(m, "ip"),
				ReceivedAt: jstr(m, "received_at"),
			})
		}
	}
	return msgs, nil
}

// DeleteMessages clears the inbox backlog.
func (c *Client) DeleteMessages(token string) error {
	req, err := http.NewRequest("DELETE", c.BaseURL+"/v1/messages", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("relais delete messages %d", resp.StatusCode)
	}
	return nil
}

// DecisionURL builds a tap-to-decide URL.
func DecisionURL(catchURL, decision, nonce string) string {
	sep := "?"
	if strings.Contains(catchURL, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%sd=%s&n=%s", catchURL, sep, decision, nonce)
}

// NewNonce returns a 16-byte hex nonce.
func NewNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// isOK accepts bool, number 1, or string "true" as truthy.
func isOK(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case json.Number:
		return x == "1" || x == "true"
	case string:
		return x == "1" || x == "true"
	case int, int64, float64:
		return x == 1
	}
	return false
}

// jstr returns a string from a generic JSON object.
func jstr(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	return ""
}
