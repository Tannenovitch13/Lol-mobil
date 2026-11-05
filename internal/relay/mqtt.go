package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/tannenovitch13/lol-mobil/internal/mqtt"
)

// DefaultBrokers are free public MQTT brokers (WebSocket over TLS). No
// account is needed; they only ever see the room id and ciphertext.
var DefaultBrokers = []string{
	"wss://broker.hivemq.com:8884/mqtt",
	"wss://broker.emqx.io:8084/mqtt",
	"wss://test.mosquitto.org:8081/mqtt",
}

// Topics used in a room (same in web/js/mqtt.js and Remote.kt):
//
//	lolmobil/v1/<room>/host      phone -> PC frames {"t":"open"|"msg"|"close","c":id,"d":ciphertext}
//	lolmobil/v1/<room>/c/<id>    PC -> phone ciphertext
//	lolmobil/v1/<room>/presence  retained "1" while the PC is connected, "0" otherwise
func topicPrefix(room string) string { return "lolmobil/v1/" + room }

// MQTTHost makes the companion reachable through public MQTT brokers. It
// listens on every broker at once, so a phone can use whichever one its
// network reaches.
type MQTTHost struct {
	Brokers    []string
	Keys       *Keys
	NewSession func(id string, send func(data []byte) error) Session
	OnStatus   func(connected bool)

	mu        sync.Mutex
	connected map[string]bool
}

// Connected reports whether at least one broker is connected.
func (h *MQTTHost) Connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ok := range h.connected {
		if ok {
			return true
		}
	}
	return false
}

func (h *MQTTHost) setConnected(broker string, ok bool) {
	h.mu.Lock()
	if h.connected == nil {
		h.connected = map[string]bool{}
	}
	before := false
	for _, v := range h.connected {
		before = before || v
	}
	h.connected[broker] = ok
	after := false
	for _, v := range h.connected {
		after = after || v
	}
	h.mu.Unlock()
	if before != after && h.OnStatus != nil {
		h.OnStatus(after)
	}
}

// Run connects to every broker until ctx is cancelled.
func (h *MQTTHost) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, b := range h.Brokers {
		wg.Add(1)
		go func(broker string) {
			defer wg.Done()
			h.runBroker(ctx, broker)
		}(b)
	}
	wg.Wait()
}

func randomID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (h *MQTTHost) runBroker(ctx context.Context, broker string) {
	prefix := topicPrefix(h.Keys.RoomID)
	presence := prefix + "/presence"
	retry := 3 * time.Second
	logged := false
	for ctx.Err() == nil {
		c, err := mqtt.Dial(ctx, mqtt.Options{
			URL:         broker,
			ClientID:    randomID("lmh-"),
			KeepAlive:   30 * time.Second,
			WillTopic:   presence,
			WillPayload: []byte("0"),
			WillRetain:  true,
		})
		if err != nil {
			if !logged {
				log.Printf("[Remote] Broker %s unreachable: %v", broker, err)
				logged = true
			}
			sleep(ctx, retry)
			retry = min(retry*2, 2*time.Minute)
			continue
		}
		logged = false
		retry = 3 * time.Second
		if err := c.Subscribe(prefix + "/host"); err != nil {
			c.Close()
			continue
		}
		_ = c.Publish(presence, []byte("1"), true)
		log.Printf("[Remote] Remote access ready through %s", broker)
		h.setConnected(broker, true)

		sessions := map[string]Session{}
		var order []string
		var smu sync.Mutex
		open := func(id string) Session {
			smu.Lock()
			defer smu.Unlock()
			if s := sessions[id]; s != nil {
				return s
			}
			// Unknown ids cost little, but keep their number bounded.
			if len(order) >= 16 {
				oldest := order[0]
				order = order[1:]
				if s := sessions[oldest]; s != nil {
					delete(sessions, oldest)
					go s.Close()
				}
			}
			topic := prefix + "/c/" + id
			s := h.NewSession(id, func(data []byte) error {
				return c.Publish(topic, []byte(h.Keys.Seal(data)), false)
			})
			sessions[id] = s
			order = append(order, id)
			return s
		}

		// On shutdown, mark the PC offline before leaving (the last will
		// would only fire after the keepalive).
		runCtx, stopRun := context.WithCancel(context.Background())
		go func() {
			select {
			case <-ctx.Done():
				_ = c.Publish(presence, []byte("0"), true)
				c.Close()
			case <-runCtx.Done():
			}
		}()
		err = c.Run(runCtx, func(m mqtt.Message) {
			var f frame
			if json.Unmarshal(m.Payload, &f) != nil || f.C == "" || len(f.C) > 64 {
				return
			}
			switch f.T {
			case "open":
				smu.Lock()
				old := sessions[f.C]
				delete(sessions, f.C)
				smu.Unlock()
				if old != nil {
					old.Close()
				}
				open(f.C)
			case "msg":
				plain, err := h.Keys.Open(f.D)
				if err != nil {
					return
				}
				open(f.C).Deliver(plain)
			case "close":
				smu.Lock()
				s := sessions[f.C]
				delete(sessions, f.C)
				smu.Unlock()
				if s != nil {
					s.Close()
				}
			}
		})

		stopRun()
		smu.Lock()
		for _, s := range sessions {
			go s.Close()
		}
		smu.Unlock()
		h.setConnected(broker, false)
		if ctx.Err() != nil {
			return
		}
		c.Close()
		log.Printf("[Remote] Lost broker %s (%v), reconnecting", broker, err)
		sleep(ctx, 2*time.Second)
	}
}
