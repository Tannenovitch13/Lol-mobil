// Package pengu talks to the LolMobil-Remote Pengu Loader plugin running
// inside the League client, and installs that plugin next to Rose's plugins.
//
// The plugin is what makes skin selection compatible with Rose: Rose learns
// the skin to inject from the name shown in the champion select carousel
// (its ROSE-SkinMonitor plugin reads it), so the phone's choice has to be
// made in that carousel. The plugin scrolls the carousel to the requested
// skin; Rose then sees it exactly as if it had been clicked by hand.
package pengu

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Hub accepts the plugin's websocket connection and sends it commands.
type Hub struct {
	// OnStatus is called when the plugin connects or disconnects.
	OnStatus func(connected bool, version string)
	// OnMessage receives unsolicited plugin messages (e.g. carousel state).
	OnMessage func(raw json.RawMessage)

	mu      sync.Mutex
	conn    *websocket.Conn
	version string
	writeMu sync.Mutex
	nextID  atomic.Int64
	pending sync.Map // id -> chan json.RawMessage
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// The plugin runs in the League client, whose pages have a
	// https://127.0.0.1 origin. Only loopback peers are accepted (below).
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Connected reports whether a plugin is connected.
func (h *Hub) Connected() (bool, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conn != nil, h.version
}

// ServeHTTP handles /plugin. Only connections from this PC are accepted.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)

	h.mu.Lock()
	old := h.conn
	h.conn = conn
	h.version = ""
	h.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	log.Printf("[Plugin] LolMobil-Remote plugin connected")

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		h.handle(conn, data)
	}

	h.mu.Lock()
	current := h.conn == conn
	if current {
		h.conn = nil
		h.version = ""
	}
	h.mu.Unlock()
	_ = conn.Close()
	if current {
		log.Printf("[Plugin] LolMobil-Remote plugin disconnected")
		if h.OnStatus != nil {
			h.OnStatus(false, "")
		}
	}
}

func (h *Hub) handle(conn *websocket.Conn, data []byte) {
	var head struct {
		Type    string `json:"type"`
		ID      int64  `json:"id"`
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &head) != nil {
		return
	}
	switch head.Type {
	case "hello":
		h.mu.Lock()
		if h.conn == conn {
			h.version = head.Version
		}
		h.mu.Unlock()
		log.Printf("[Plugin] Plugin version %s", head.Version)
		if h.OnStatus != nil {
			h.OnStatus(true, head.Version)
		}
	case "result":
		if ch, ok := h.pending.LoadAndDelete(head.ID); ok {
			ch.(chan json.RawMessage) <- json.RawMessage(append([]byte(nil), data...))
		}
	case "ping":
		_ = h.write(map[string]any{"type": "pong"})
	default:
		if h.OnMessage != nil {
			h.OnMessage(json.RawMessage(append([]byte(nil), data...)))
		}
	}
}

func (h *Hub) write(msg any) error {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		return ErrNoPlugin
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, data)
}

// ErrNoPlugin means the plugin is not connected.
var ErrNoPlugin = errors.New("the LolMobil-Remote plugin is not connected (is Pengu Loader / Rose active?)")

// Call sends a command (a JSON object with a "type") and waits for the
// plugin's result.
func (h *Hub) Call(ctx context.Context, cmd map[string]any) (json.RawMessage, error) {
	id := h.nextID.Add(1)
	cmd["id"] = id
	ch := make(chan json.RawMessage, 1)
	h.pending.Store(id, ch)
	defer h.pending.Delete(id)
	if err := h.write(cmd); err != nil {
		return nil, err
	}
	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
