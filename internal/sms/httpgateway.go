package sms

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPGateway is an alternative to a dedicated GSM modem: instead of a USB
// serial modem, an ordinary Android phone (with a SIM and signal) runs a
// small local app or script that exposes SMS send/receive over plain HTTP
// on the same offline LAN/hotspot as this kiosk. That's usually cheaper and
// easier to get hold of than dedicated modem hardware - see
// docs/phone-sms-gateway/ in this repo for a ready-to-run Termux script
// implementing this exact contract on a spare Android phone.
//
// Contract expected of the gateway (see docs/phone-sms-gateway/README.md
// for the full spec and a reference implementation):
//
//	POST {baseURL}/send            {"to": "...", "message": "..."} -> {"success": true}
//	GET  {baseURL}/inbox/unread    -> [{"id": "...", "from": "...", "body": "..."}]
//	POST {baseURL}/inbox/ack       {"id": "..."} -> {"success": true}
//
// An optional bearer token can be required by the gateway for a small
// amount of protection against other devices on the same local network.
type HTTPGateway struct {
	baseURL string
	token   string
	client  *http.Client
}

// OpenHTTPGateway connects to a phone-based SMS gateway at baseURL (e.g.
// "http://192.168.1.50:8090"). It doesn't make any network calls itself -
// connectivity is only checked when you actually send/read messages.
func OpenHTTPGateway(baseURL, token string) *HTTPGateway {
	return &HTTPGateway{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

var _ Transport = (*HTTPGateway)(nil)

func (g *HTTPGateway) Close() error { return nil }

func (g *HTTPGateway) doJSON(method, path string, body interface{}, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, g.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach phone gateway at %s: %w", g.baseURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("phone gateway returned %s: %s", resp.Status, string(respBody))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("could not parse phone gateway response: %w", err)
		}
	}
	return nil
}

func (g *HTTPGateway) SendSMS(number, message string) error {
	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := g.doJSON(http.MethodPost, "/send", map[string]string{"to": number, "message": message}, &result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("phone gateway rejected the message: %s", result.Error)
	}
	return nil
}

func (g *HTTPGateway) ReadUnread() ([]IncomingMessage, error) {
	var messages []IncomingMessage
	if err := g.doJSON(http.MethodGet, "/inbox/unread", nil, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (g *HTTPGateway) Ack(id string) error {
	var result struct {
		Success bool `json:"success"`
	}
	return g.doJSON(http.MethodPost, "/inbox/ack", map[string]string{"id": id}, &result)
}
