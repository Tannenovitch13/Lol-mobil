// Package rose connects to the local WebSocket bridge of Rose
// (https://github.com/Alban1911/Rose), the League skin changer, to read the
// skin it will inject and to forward chroma choices made on the phone.
//
// Rose's bridge listens on the first free port of 127.0.0.1:50000-50099 and
// answers GET /bridge-port with the port it uses. Clients that send no
// browser Origin header (like this one) are accepted.
package rose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	firstPort = 50000
	portCount = 100
)

// ForwardedTypes are the Rose broadcasts relayed to the phone.
var ForwardedTypes = map[string]bool{
	"skin-state":        true,
	"chroma-state":      true,
	"phase-change":      true,
	"champion-locked":   true,
	"random-mode-state": true,
	"historic-state":    true,
	"custom-mod-state":  true,
	"skip-base-skin":    true,
}

// Bridge keeps a connection to Rose.
type Bridge struct {
	// OnMessage receives every forwarded Rose message.
	OnMessage func(msgType string, raw json.RawMessage)
	// OnStatus is called on connect/disconnect.
	OnStatus func(connected bool, port int)

	mu        sync.Mutex
	conn      *websocket.Conn
	port      int
	lastState map[string]json.RawMessage
	writeMu   sync.Mutex
}

// Connected reports whether Rose's bridge is reachable.
func (b *Bridge) Connected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn != nil
}

// Snapshot returns the last message of every forwarded type.
func (b *Bridge) Snapshot() map[string]json.RawMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]json.RawMessage, len(b.lastState))
	for k, v := range b.lastState {
		out[k] = v
	}
	return out
}

// Send writes a JSON message to Rose.
func (b *Bridge) Send(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.mu.Lock()
	conn := b.conn
	b.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("Rose is not running")
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, data)
}

// Run keeps trying to connect until ctx is cancelled.
func (b *Bridge) Run(ctx context.Context) {
	logged := false
	for ctx.Err() == nil {
		port := discoverPort(ctx)
		if port == 0 {
			sleep(ctx, 3*time.Second)
			continue
		}
		dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
		conn, _, err := dialer.DialContext(ctx, fmt.Sprintf("ws://127.0.0.1:%d/", port), nil)
		if err != nil {
			if !logged {
				log.Printf("[Rose] Bridge found on port %d but connection failed: %v", port, err)
				logged = true
			}
			sleep(ctx, 3*time.Second)
			continue
		}
		logged = false
		conn.SetReadLimit(16 << 20)
		b.mu.Lock()
		b.conn = conn
		b.port = port
		b.lastState = map[string]json.RawMessage{}
		b.mu.Unlock()
		log.Printf("[Rose] Connected to Rose's bridge on port %d", port)
		if b.OnStatus != nil {
			b.OnStatus(true, port)
		}

		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-done:
			}
		}()
		b.readLoop(conn)
		close(done)

		b.mu.Lock()
		b.conn = nil
		b.lastState = nil
		b.mu.Unlock()
		_ = conn.Close()
		log.Printf("[Rose] Disconnected from Rose's bridge")
		if b.OnStatus != nil {
			b.OnStatus(false, 0)
		}
		sleep(ctx, 2*time.Second)
	}
}

func (b *Bridge) readLoop(conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &head) != nil || !ForwardedTypes[head.Type] {
			continue
		}
		raw := json.RawMessage(append([]byte(nil), data...))
		b.mu.Lock()
		if b.lastState != nil {
			b.lastState[head.Type] = raw
		}
		b.mu.Unlock()
		if b.OnMessage != nil {
			b.OnMessage(head.Type, raw)
		}
	}
}

// discoverPort probes Rose's port range and returns the bridge port, or 0.
func discoverPort(ctx context.Context) int {
	client := &http.Client{Timeout: 600 * time.Millisecond}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	found := make(chan int, portCount)
	var wg sync.WaitGroup
	for p := firstPort; p < firstPort+portCount; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/bridge-port", p), nil)
			if err != nil {
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 32))
			if port, err := strconv.Atoi(strings.TrimSpace(string(body))); err == nil && port > 0 {
				found <- port
			}
		}(p)
	}
	go func() {
		wg.Wait()
		close(found)
	}()
	for port := range found {
		return port
	}
	return 0
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
