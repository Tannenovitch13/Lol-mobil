// Package mqtt is a minimal MQTT 3.1.1 client over WebSocket: QoS 0
// publish/subscribe, retained messages, last will and keepalive. That is all
// LolMobil needs to meet the phone on a public broker (remote access without
// any server of your own). Messages are end-to-end encrypted above this
// layer.
package mqtt

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Options to connect.
type Options struct {
	URL         string // ws://host:port/path or wss://...
	ClientID    string
	KeepAlive   time.Duration
	WillTopic   string
	WillPayload []byte
	WillRetain  bool
}

// Message is a received publication.
type Message struct {
	Topic   string
	Payload []byte
}

// Client is one broker connection.
type Client struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex
	keepAlive time.Duration
	nextID    uint16
	buf       []byte
}

const (
	typeConnect    = 1
	typeConnack    = 2
	typePublish    = 3
	typePuback     = 4
	typeSubscribe  = 8
	typeSuback     = 9
	typePingreq    = 12
	typePingresp   = 13
	typeDisconnect = 14
)

func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}

func appendBytes(b []byte, p []byte) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(p)))
	return append(b, p...)
}

func packet(header byte, body []byte) []byte {
	out := []byte{header}
	n := len(body)
	for {
		d := byte(n % 128)
		n /= 128
		if n > 0 {
			d |= 0x80
		}
		out = append(out, d)
		if n == 0 {
			break
		}
	}
	return append(out, body...)
}

// Dial connects and waits for the broker's CONNACK.
func Dial(ctx context.Context, o Options) (*Client, error) {
	if o.KeepAlive == 0 {
		o.KeepAlive = 30 * time.Second
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		Subprotocols:     []string{"mqtt"},
		Proxy:            http.ProxyFromEnvironment,
	}
	conn, resp, err := dialer.DialContext(ctx, o.URL, http.Header{})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("%w (HTTP %d)", err, resp.StatusCode)
		}
		return nil, err
	}
	conn.SetReadLimit(64 << 20)
	c := &Client{conn: conn, keepAlive: o.KeepAlive}

	flags := byte(0x02) // clean session
	body := appendString(nil, "MQTT")
	body = append(body, 4) // protocol level 3.1.1
	if o.WillTopic != "" {
		flags |= 0x04
		if o.WillRetain {
			flags |= 0x20
		}
	}
	body = append(body, flags)
	body = binary.BigEndian.AppendUint16(body, uint16(o.KeepAlive/time.Second))
	body = appendString(body, o.ClientID)
	if o.WillTopic != "" {
		body = appendString(body, o.WillTopic)
		body = appendBytes(body, o.WillPayload)
	}
	if err := c.write(packet(typeConnect<<4, body)); err != nil {
		conn.Close()
		return nil, err
	}

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	kind, _, payload, err := c.readPacket()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if kind != typeConnack || len(payload) < 2 || payload[1] != 0 {
		conn.Close()
		return nil, errors.New("connection refused by the broker")
	}
	_ = conn.SetReadDeadline(time.Time{})
	return c, nil
}

func (c *Client) write(p []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return c.conn.WriteMessage(websocket.BinaryMessage, p)
}

// readPacket returns the next MQTT packet (WebSocket frames may split or
// group packets).
func (c *Client) readPacket() (kind byte, flags byte, body []byte, err error) {
	for {
		if len(c.buf) >= 2 {
			length, used, ok := remainingLength(c.buf[1:])
			if ok && len(c.buf) >= 1+used+length {
				header := c.buf[0]
				body = append([]byte(nil), c.buf[1+used:1+used+length]...)
				c.buf = c.buf[1+used+length:]
				return header >> 4, header & 0x0f, body, nil
			}
		}
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return 0, 0, nil, err
		}
		c.buf = append(c.buf, data...)
	}
}

func remainingLength(b []byte) (length, used int, ok bool) {
	mult := 1
	for i := 0; i < 4 && i < len(b); i++ {
		length += int(b[i]&0x7f) * mult
		if b[i]&0x80 == 0 {
			return length, i + 1, true
		}
		mult *= 128
	}
	return 0, 0, false
}

// Subscribe subscribes to a topic filter with QoS 0.
func (c *Client) Subscribe(topic string) error {
	c.nextID++
	if c.nextID == 0 {
		c.nextID = 1
	}
	body := binary.BigEndian.AppendUint16(nil, c.nextID)
	body = appendString(body, topic)
	body = append(body, 0)
	return c.write(packet(typeSubscribe<<4|0x02, body))
}

// Publish sends a QoS 0 message.
func (c *Client) Publish(topic string, payload []byte, retain bool) error {
	header := byte(typePublish << 4)
	if retain {
		header |= 0x01
	}
	body := appendString(nil, topic)
	body = append(body, payload...)
	return c.write(packet(header, body))
}

// Run reads until the connection ends, calling onMessage for each
// publication, and keeps the connection alive.
func (c *Client) Run(ctx context.Context, onMessage func(Message)) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(c.keepAlive * 2 / 3)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				_ = c.conn.Close()
				return
			case <-t.C:
				if c.write([]byte{typePingreq << 4, 0}) != nil {
					return
				}
			}
		}
	}()
	for {
		// The broker answers pings within the keepalive: silence means a
		// dead connection.
		_ = c.conn.SetReadDeadline(time.Now().Add(c.keepAlive * 2))
		kind, flags, body, err := c.readPacket()
		if err != nil {
			return err
		}
		if kind != typePublish || len(body) < 2 {
			continue
		}
		n := int(binary.BigEndian.Uint16(body))
		if len(body) < 2+n {
			continue
		}
		topic := string(body[2 : 2+n])
		rest := body[2+n:]
		if qos := (flags >> 1) & 0x03; qos > 0 && len(rest) >= 2 {
			id := rest[:2]
			rest = rest[2:]
			_ = c.write(append([]byte{typePuback << 4, 2}, id...))
		}
		onMessage(Message{Topic: topic, Payload: bytes.Clone(rest)})
	}
}

// Close disconnects cleanly (no last will).
func (c *Client) Close() {
	_ = c.write([]byte{typeDisconnect << 4, 0})
	_ = c.conn.Close()
}
