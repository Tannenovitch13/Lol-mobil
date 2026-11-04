package main

import (
	"log"
	"log/slog"
	"os"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

// runBroker starts a local MQTT broker (WebSocket) standing in for the public
// brokers used by remote access, for tests.
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
	log.Printf("mock MQTT broker on ws://%s/mqtt", addr)
	if err := server.Serve(); err != nil {
		log.Printf("mock broker: %v", err)
	}
}
