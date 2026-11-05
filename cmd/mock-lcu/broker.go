package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

var (
	brokersMu sync.Mutex
	brokers   = map[string]*mqtt.Server{}
)

// runBrokers starts local MQTT brokers (WebSocket) standing in for the
// public brokers used by remote access, for tests: one per address of the
// comma-separated list.
func runBrokers(list string) {
	for _, addr := range strings.Split(list, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			go runBroker(addr)
		}
	}
}

func runBroker(addr string) {
	server := mqtt.New(&mqtt.Options{
		InlineClient: false,
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err := server.AddHook(new(auth.AllowHook), nil); err != nil {
		log.Fatal(err)
	}
	if err := server.AddListener(listeners.NewWebsocket(listeners.Config{ID: "ws", Address: addr})); err != nil {
		log.Fatal(err)
	}
	brokersMu.Lock()
	brokers[addr] = server
	brokersMu.Unlock()
	log.Printf("mock MQTT broker on ws://%s/mqtt", addr)
	if err := server.Serve(); err != nil {
		log.Printf("mock broker: %v", err)
	}
}

// stopBroker (GET /mock/broker-stop?addr=127.0.0.1:1884) shuts a broker
// down, like a public broker dropping everyone: tests the phone's switch to
// another broker.
func stopBroker(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("addr")
	brokersMu.Lock()
	server := brokers[addr]
	delete(brokers, addr)
	brokersMu.Unlock()
	if server == nil {
		writeJSON(w, 404, map[string]any{"message": "no broker on " + addr})
		return
	}
	_ = server.Close()
	log.Printf("mock MQTT broker on %s stopped", addr)
	writeJSON(w, 200, map[string]any{"ok": true})
}
