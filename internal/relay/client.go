package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Session is one phone connected through the relay.
type Session interface {
	// Deliver passes a decrypted message from the phone.
	Deliver(data []byte)
	// Close ends the session (phone gone or relay lost).
	Close()
}

// Host keeps the companion connected to the relay as the room's host.
type Host struct {
	// URL is the relay base URL (https://lolmobil-relay.<you>.workers.dev).
	URL  string
	Keys *Keys
	// NewSession is called when a phone joins; send writes an encrypted
	// message to that phone.
	NewSession func(id string, send func(data []byte) error) Session
	// OnStatus reports whether the relay connection is up.
	OnStatus func(connected bool)

	mu       sync.Mutex
	conn     *websocket.Conn
	writeMu  sync.Mutex
	sessions map[string]Session
}

type frame struct {
	T      string `json:"t"`
	C      string `json:"c,omitempty"`
	D      string `json:"d,omitempty"`
	Online *bool  `json:"online,omitempty"`
}

// Connected reports whether the relay connection is up.
func (h *Host) Connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn != nil
}

func (h *Host) hostURL() (string, error) {
	u, err := url.Parse(strings.TrimRight(h.URL, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid relay URL %q", h.URL)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/host/" + h.Keys.RoomID
	u.RawQuery = "auth=" + h.Keys.HostAuth
	return u.String(), nil
}

// Run keeps the relay connection alive until ctx is cancelled.
func (h *Host) Run(ctx context.Context) {
	target, err := h.hostURL()
	if err != nil {
		log.Printf("[Relay] %v", err)
		return
	}
	retry := time.Second
	logged := false
	for ctx.Err() == nil {
		dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
		conn, resp, err := dialer.DialContext(ctx, target, nil)
		if err != nil {
			if !logged {
				status := ""
				if resp != nil {
					status = fmt.Sprintf(" (HTTP %d)", resp.StatusCode)
				}
				log.Printf("[Relay] Cannot reach the relay %s%s: %v", h.URL, status, err)
				logged = true
			}
			sleep(ctx, retry)
			retry = min(retry*2, time.Minute)
			continue
		}
		logged = false
		retry = time.Second
		conn.SetReadLimit(32 << 20)
		h.mu.Lock()
		h.conn = conn
		h.sessions = map[string]Session{}
		h.mu.Unlock()
		log.Printf("[Relay] Remote access ready through %s", h.URL)
		if h.OnStatus != nil {
			h.OnStatus(true)
		}

		done := make(chan struct{})
		go h.keepAlive(conn, done)
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-done:
			}
		}()
		h.readLoop(conn)
		close(done)

		h.mu.Lock()
		sessions := h.sessions
		h.sessions = nil
		h.conn = nil
		h.mu.Unlock()
		for _, s := range sessions {
			s.Close()
		}
		_ = conn.Close()
		if h.OnStatus != nil {
			h.OnStatus(false)
		}
		if ctx.Err() == nil {
			log.Printf("[Relay] Connection to the relay lost, reconnecting")
			sleep(ctx, 2*time.Second)
		}
	}
}

func (h *Host) keepAlive(conn *websocket.Conn, done chan struct{}) {
	t := time.NewTicker(25 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			// Answered by the relay itself without waking the room.
			if err := h.write(conn, []byte("ping")); err != nil {
				return
			}
		}
	}
}

func (h *Host) write(conn *websocket.Conn, data []byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, data)
}

func (h *Host) readLoop(conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var f frame
		if json.Unmarshal(data, &f) != nil {
			continue // "pong"
		}
		switch f.T {
		case "open":
			h.open(conn, f.C)
		case "close":
			h.mu.Lock()
			s := h.sessions[f.C]
			delete(h.sessions, f.C)
			h.mu.Unlock()
			if s != nil {
				s.Close()
			}
		case "msg":
			h.mu.Lock()
			s := h.sessions[f.C]
			h.mu.Unlock()
			if s == nil {
				continue
			}
			plain, err := h.Keys.Open(f.D)
			if err != nil {
				// Not encrypted with our secret: ignore.
				continue
			}
			s.Deliver(plain)
		}
	}
}

func (h *Host) open(conn *websocket.Conn, id string) {
	if id == "" || h.NewSession == nil {
		return
	}
	send := func(data []byte) error {
		out, err := json.Marshal(frame{T: "msg", C: id, D: h.Keys.Seal(data)})
		if err != nil {
			return err
		}
		return h.write(conn, out)
	}
	s := h.NewSession(id, send)
	h.mu.Lock()
	if h.sessions != nil {
		if old := h.sessions[id]; old != nil {
			defer old.Close()
		}
		h.sessions[id] = s
	}
	h.mu.Unlock()
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
