package server

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"strings"
)

// DiscoveryMessage is what the phone broadcasts to find companions.
const DiscoveryMessage = "LOLMOBIL_DISCOVER"

// RunDiscovery answers the phone's UDP broadcasts on udpPort with this
// companion's name and HTTP port, so the app can list PCs on the Wi-Fi.
func (s *Server) RunDiscovery(ctx context.Context, udpPort int) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: udpPort})
	if err != nil {
		log.Printf("[Discovery] Automatic discovery disabled (UDP %d: %v). The phone can still connect with the PC's address.", udpPort, err)
		return
	}
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	reply, _ := json.Marshal(map[string]any{
		"app":     "lolmobil",
		"name":    s.Name,
		"port":    s.Cfg.Port,
		"version": s.Version,
	})
	buf := make([]byte, 512)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if strings.TrimSpace(string(buf[:n])) != DiscoveryMessage {
			continue
		}
		_, _ = conn.WriteToUDP(reply, addr)
	}
}
