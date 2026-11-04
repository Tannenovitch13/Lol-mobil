package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// runRose imitates the parts of Rose's bridge LolMobil uses: port discovery,
// skin detection messages ({"skin": name}) answered with "skin-state", and
// "chroma-selection" answered with "chroma-state".
func runRose() {
	var mu sync.Mutex
	conns := map[*websocket.Conn]bool{}
	broadcast := func(v any) {
		data, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		for c := range conns {
			_ = c.WriteMessage(websocket.TextMessage, data)
		}
	}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/bridge-port", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write([]byte("50000"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			http.NotFound(w, r)
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		conns[conn] = true
		mu.Unlock()
		defer func() {
			mu.Lock()
			delete(conns, conn)
			mu.Unlock()
			conn.Close()
		}()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if msg["type"] == "chroma-selection" {
				log.Printf("[mock Rose] chroma %v", msg["chromaId"])
				broadcast(map[string]any{"type": "chroma-state", "selectedChromaId": msg["chromaId"], "timestamp": time.Now().UnixMilli()})
				continue
			}
			if name, ok := msg["skin"].(string); ok && name != "" {
				id, champID := findSkin(name)
				log.Printf("[mock Rose] skin detected %q -> %d", name, id)
				broadcast(map[string]any{"type": "skin-state", "skinName": name, "skinId": id, "championId": champID, "hasChromas": false})
			}
		}
	})
	log.Printf("mock Rose bridge on http://127.0.0.1:50000")
	if err := http.ListenAndServe("127.0.0.1:50000", mux); err != nil {
		log.Printf("mock Rose: %v", err)
	}
}

func findSkin(name string) (int, int) {
	for _, c := range champions {
		for _, s := range carouselSkins(c.ID) {
			if strings.EqualFold(s["name"].(string), name) {
				return s["id"].(int), c.ID
			}
		}
	}
	return 0, 0
}
