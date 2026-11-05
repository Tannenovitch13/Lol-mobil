package server

import (
	"testing"

	"github.com/tannenovitch13/lol-mobil/internal/config"
	"github.com/tannenovitch13/lol-mobil/internal/lcu"
	"github.com/tannenovitch13/lol-mobil/internal/pengu"
	"github.com/tannenovitch13/lol-mobil/internal/rose"
)

// A phone on remote access learns how to reach the PC on its Wi-Fi, so the
// app can connect directly at home; a phone already on the Wi-Fi does not.
func TestHelloGivesRemotePhonesTheWiFi(t *testing.T) {
	old := lanAddresses
	lanAddresses = func() []string { return []string{"192.168.1.20", "10.0.0.5"} }
	defer func() { lanAddresses = old }()
	s := &Server{
		Cfg:    &config.Config{Port: 8787, Token: "0123456789abcdef0123"},
		LCU:    &lcu.Connector{},
		Rose:   &rose.Bridge{},
		Plugin: &pengu.Hub{},
	}
	remote := s.helloMessage(&client{remote: true})
	lan, ok := remote["lan"].(map[string]any)
	if !ok {
		t.Fatalf("no Wi-Fi details for a remote phone: %v", remote)
	}
	urls, _ := lan["urls"].([]string)
	if len(urls) != 2 || urls[0] != "http://192.168.1.20:8787" || lan["token"] != "0123456789abcdef0123" {
		t.Fatalf("unexpected Wi-Fi details %v", lan)
	}
	if _, ok := s.helloMessage(&client{})["lan"]; ok {
		t.Fatal("Wi-Fi details sent to a phone already on the Wi-Fi")
	}
}
