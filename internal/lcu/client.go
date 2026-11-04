package lcu

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Client talks to one running League client.
type Client struct {
	creds Credentials
	auth  string
	http  *http.Client
}

// tlsConfig accepts the client's certificate. The LCU only listens on
// 127.0.0.1 and uses a certificate signed by Riot's private root, so the
// usual verification cannot succeed; the connection never leaves the PC.
func tlsConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // loopback only, see above
}

// NewClient builds a client for the given credentials.
func NewClient(creds Credentials) *Client {
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig(),
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     60 * time.Second,
		DialContext:         (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
	}
	return &Client{
		creds: creds,
		auth:  "Basic " + base64.StdEncoding.EncodeToString([]byte("riot:"+creds.Password)),
		http:  &http.Client{Transport: transport, Timeout: 20 * time.Second},
	}
}

// Credentials returns the credentials the client uses.
func (c *Client) Credentials() Credentials { return c.creds }

// Response is a raw LCU answer.
type Response struct {
	Status int
	Body   json.RawMessage
}

// Do sends a request. body may be nil. A JSON body is expected.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) (Response, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.creds.URL()+path, reader)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Response{}, err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || !json.Valid(data) {
		if len(data) == 0 {
			data = []byte("null")
		} else {
			quoted, _ := json.Marshal(string(data))
			data = quoted
		}
	}
	return Response{Status: resp.StatusCode, Body: data}, nil
}

// GetJSON fetches a path and decodes a 2xx answer into out.
func (c *Client) GetJSON(ctx context.Context, path string, out any) error {
	resp, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if resp.Status < 200 || resp.Status > 299 {
		return fmt.Errorf("GET %s: HTTP %d", path, resp.Status)
	}
	return json.Unmarshal(resp.Body, out)
}

// Raw performs a GET and returns the live response (for images). The caller
// closes the body.
func (c *Client) Raw(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.creds.URL()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.auth)
	return c.http.Do(req)
}

// Event is one LCU websocket event.
type Event struct {
	URI       string          `json:"uri"`
	EventType string          `json:"eventType"` // Create, Update, Delete
	Data      json.RawMessage `json:"data"`
}

// Listen connects to the WAMP websocket, subscribes to every JSON API event
// and calls handler for each one. It blocks until the connection ends or ctx
// is cancelled.
func (c *Client) Listen(ctx context.Context, handler func(Event)) error {
	base, err := url.Parse(c.creds.URL())
	if err != nil {
		return err
	}
	if base.Scheme == "https" {
		base.Scheme = "wss"
	} else {
		base.Scheme = "ws"
	}
	base.Path = "/"
	dialer := websocket.Dialer{
		TLSClientConfig:  tlsConfig(),
		HandshakeTimeout: 5 * time.Second,
		Subprotocols:     []string{"wamp"},
	}
	header := http.Header{}
	header.Set("Authorization", c.auth)
	conn, _, err := dialer.DialContext(ctx, base.String(), header)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetReadLimit(64 << 20)

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`[5,"OnJsonApiEvent"]`)); err != nil {
		return err
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		var frame []json.RawMessage
		if json.Unmarshal(msg, &frame) != nil || len(frame) < 3 {
			continue
		}
		var opcode int
		if json.Unmarshal(frame[0], &opcode) != nil || opcode != 8 {
			continue
		}
		var ev Event
		if json.Unmarshal(frame[2], &ev) != nil || ev.URI == "" {
			continue
		}
		handler(ev)
	}
}

// IsTransient reports whether an error looks like the client went away.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "EOF") ||
		strings.Contains(s, "reset") || strings.Contains(s, "closed")
}
