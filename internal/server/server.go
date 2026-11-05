// Package server is what the phone connects to: it serves the mobile web
// interface and a websocket that relays the League client API, Rose's state
// and skin selection through the LolMobil-Remote plugin.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/tannenovitch13/lol-mobil/internal/builds"
	"github.com/tannenovitch13/lol-mobil/internal/config"
	"github.com/tannenovitch13/lol-mobil/internal/lcu"
	"github.com/tannenovitch13/lol-mobil/internal/pengu"
	"github.com/tannenovitch13/lol-mobil/internal/rose"
)

// Server is the phone-facing HTTP and websocket server.
type Server struct {
	Cfg     *config.Config
	LCU     *lcu.Connector
	Rose    *rose.Bridge
	Plugin  *pengu.Hub
	Web     fs.FS
	Version string
	Name    string // shown on the phone (PC name)
	// RelayConnected reports whether remote access is up (nil when off).
	RelayConnected func() bool
	// RemoteLink is the remote access link (secret in the fragment) and
	// RemoteVia describes the way it goes, for the pairing page.
	RemoteLink string
	RemoteVia  string

	mu      sync.Mutex
	clients map[*client]struct{}

	pairMu    sync.Mutex
	pairFails []time.Time

	readyMu       sync.Mutex
	readyAccepted bool

	// Builds fetches OP.GG / Blitz builds (nil: feature off).
	Builds        *builds.Service
	autoBuildMu   sync.Mutex
	autoBuildDone string
	itemsMu       sync.Mutex
	finalItemIDs  map[int]bool // completed items (client data, per session)
	staticData    staticCache  // trimmed game data, per client session
}

// client is one connected phone, on the Wi-Fi (websocket) or through the
// relay. Messages to the phone go through send; a transport-specific writer
// drains it.
type client struct {
	s        *Server
	remote   bool
	send     chan []byte
	mu       sync.Mutex
	observed []string
	closed   bool
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	// The token is what protects the socket; the page may be opened from the
	// PC's LAN address or loaded inside the Android app.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.serveWS)
	mux.Handle("/plugin", s.Plugin)
	mux.HandleFunc("/lcu/", s.serveAsset)
	mux.HandleFunc("/api/info", s.serveInfo)
	// The page asks this to know whether a relay or the companion serves it.
	mux.HandleFunc("/relay-info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"app": "lolmobil-companion"})
	})
	mux.HandleFunc("/api/pair", s.servePair)
	mux.HandleFunc("/api/check", s.serveCheck)
	mux.HandleFunc("/api/ready-check/", s.serveReadyCheck)
	mux.HandleFunc("/pair", s.servePairPage)
	mux.HandleFunc("/pair/qr.png", s.servePairQR)
	static := http.FileServer(http.FS(s.Web))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	})
	return mux
}

// ---------------------------------------------------------------- auth

func (s *Server) tokenOK(token string) bool {
	expected := s.Cfg.Token
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func requestToken(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(host).IsLoopback()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serveInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, map[string]any{
		"app":     "lolmobil",
		"name":    s.Name,
		"version": s.Version,
		"port":    s.Cfg.Port,
	})
}

func (s *Server) serveCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if !s.tokenOK(requestToken(r)) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": s.Name})
}

// servePair trades the PIN shown on the PC for the access token. Failed
// attempts are limited to 5 per 5 minutes.
func (s *Server) servePair(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	var body struct {
		PIN    string `json:"pin"`
		Device string `json:"device"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	s.pairMu.Lock()
	cutoff := time.Now().Add(-5 * time.Minute)
	kept := s.pairFails[:0]
	for _, t := range s.pairFails {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	s.pairFails = kept
	if len(s.pairFails) >= 5 {
		s.pairMu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Trop d'essais, réessaie dans quelques minutes"})
		return
	}
	pin := strings.TrimSpace(body.PIN)
	if subtle.ConstantTimeCompare([]byte(pin), []byte(s.Cfg.PIN)) != 1 {
		s.pairFails = append(s.pairFails, time.Now())
		s.pairMu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Code incorrect"})
		return
	}
	s.pairMu.Unlock()
	device := body.Device
	if device == "" {
		device = r.RemoteAddr
	}
	log.Printf("[Phone] Paired: %s", device)
	writeJSON(w, http.StatusOK, map[string]any{"token": s.Cfg.Token, "name": s.Name})
}

// serveReadyCheck lets the Android notification accept or decline a match
// with a plain HTTP call.
func (s *Server) serveReadyCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	if !s.tokenOK(requestToken(r)) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/ready-check/")
	if action != "accept" && action != "decline" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown action"})
		return
	}
	cl := s.LCU.Client()
	if cl == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "League client not connected"})
		return
	}
	resp, err := cl.Do(r.Context(), http.MethodPost, "/lol-matchmaking/v1/ready-check/"+action, nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": resp.Status})
}

// ---------------------------------------------------------------- assets

// serveAsset proxies game images (champion icons, skin tiles, rune icons)
// from the client so the phone needs no internet access.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/lcu")
	if !strings.HasPrefix(path, "/lol-game-data/assets/") || strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	cl := s.LCU.Client()
	if cl == nil {
		http.Error(w, "League client not connected", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	resp, err := cl.Raw(ctx, path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if resp.StatusCode == http.StatusOK {
		w.Header().Set("Cache-Control", "public, max-age=604800")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ---------------------------------------------------------------- websocket

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(requestToken(r)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	c := s.addClient(false)
	log.Printf("[Phone] Connected: %s", r.RemoteAddr)
	go wsWriter(conn, c.send)
	c.sendJSON(s.helloMessage(c))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		c.handle(data)
	}
	s.dropClient(c)
	log.Printf("[Phone] Disconnected: %s", r.RemoteAddr)
}

func (s *Server) addClient(remote bool) *client {
	c := &client{s: s, remote: remote, send: make(chan []byte, 512)}
	s.mu.Lock()
	if s.clients == nil {
		s.clients = map[*client]struct{}{}
	}
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	return c
}

func (s *Server) dropClient(c *client) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
	c.close()
}

// relaySession is a phone connected through the relay.
type relaySession struct{ c *client }

func (r relaySession) Deliver(data []byte) { r.c.handle(data) }
func (r relaySession) Close() {
	r.c.s.dropClient(r.c)
	log.Printf("[Phone] Remote phone disconnected")
}

// NewRelaySession registers a phone that joined through the relay. send
// encrypts and writes one message to it.
func (s *Server) NewRelaySession(id string, send func([]byte) error) relaySession {
	c := s.addClient(true)
	log.Printf("[Phone] Remote phone connected (%s)", id)
	go func() {
		for data := range c.send {
			if err := send(data); err != nil {
				return
			}
		}
	}()
	c.sendJSON(s.helloMessage(c))
	return relaySession{c}
}

func (c *client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.send)
	}
}

func (c *client) sendRaw(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.send <- data:
	default:
		// Too slow: drop the connection, the phone reconnects and resyncs.
		c.closed = true
		close(c.send)
	}
}

func (c *client) sendJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.sendRaw(data)
}

// wsWriter writes a phone's messages to its websocket (Wi-Fi mode).
func wsWriter(conn *websocket.Conn, send chan []byte) {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	defer conn.Close()
	for {
		select {
		case data, ok := <-send:
			if !ok {
				_ = conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

type inbound struct {
	ID     int64           `json:"id"`
	Type   string          `json:"type"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Paths  []string        `json:"paths"`
	Body   json.RawMessage `json:"body"`

	// select-skin / select-chroma
	SkinID     int64  `json:"skinId"`
	ChampionID int64  `json:"championId"`
	Name       string `json:"name"`
	TilePath   string `json:"tilePath"`
	Index      int    `json:"index"`
	Owned      bool   `json:"owned"`
	ChromaID   int64  `json:"chromaId"`
	BaseSkinID int64  `json:"baseSkinId"`

	// settings
	AutoAccept  *bool   `json:"autoAccept"`
	AutoBuild   *bool   `json:"autoBuild"`
	BuildSource *string `json:"buildSource"`

	// builds / apply-build
	Source     string `json:"source"`
	Position   string `json:"position"`
	Mode       string `json:"mode"`
	Tier       string `json:"tier"`
	Region     string `json:"region"`
	BuildIndex int    `json:"buildIndex"`
	WithRunes  *bool  `json:"runes"`
	WithItems  *bool  `json:"items"`
	WithSpells *bool  `json:"spells"`
}

// handle processes one message from the phone.
func (c *client) handle(data []byte) {
	var msg inbound
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	switch msg.Type {
	case "ping":
		c.sendJSON(map[string]any{"type": "pong", "id": msg.ID})
	case "request":
		go c.handleRequest(msg)
	case "observe":
		c.handleObserve(msg.Paths)
	case "unobserve":
		c.handleUnobserve(msg.Paths)
	case "select-skin":
		go c.handleSelectSkin(msg)
	case "select-chroma":
		go c.handleSelectChroma(msg)
	case "settings":
		c.handleSettings(msg)
	case "builds":
		go c.handleBuilds(msg)
	case "apply-build":
		go c.handleApplyBuild(msg)
	}
}

func (msg inbound) buildRequest() builds.Request {
	return builds.Request{
		Source: msg.Source, ChampionID: int(msg.ChampionID), Position: msg.Position,
		Mode: msg.Mode, Tier: msg.Tier, Region: msg.Region,
	}
}

// handleBuilds fetches popular builds (OP.GG, Blitz) for the phone.
func (c *client) handleBuilds(msg inbound) {
	if c.s.Builds == nil {
		c.respond(msg.ID, 503, nil, "builds indisponibles")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := c.s.Builds.Fetch(ctx, msg.buildRequest())
	if err != nil {
		c.respond(msg.ID, 502, nil, err.Error())
		return
	}
	c.respond(msg.ID, 200, mustJSON(c.s.finalItems(ctx, res)), "")
}

// finalItems keeps only completed items in the "next items" of builds.
func (s *Server) finalItems(ctx context.Context, res *builds.Result) *builds.Result {
	cl := s.LCU.Client()
	if cl == nil {
		return res
	}
	s.itemsMu.Lock()
	final := s.finalItemIDs
	s.itemsMu.Unlock()
	if final == nil {
		var err error
		if final, err = builds.FinalItems(ctx, cl); err != nil {
			return res
		}
		s.itemsMu.Lock()
		s.finalItemIDs = final
		s.itemsMu.Unlock()
	}
	return builds.WithFinalItems(res, final)
}

// handleApplyBuild writes a build (runes, item set, spells) into the client.
func (c *client) handleApplyBuild(msg inbound) {
	opt := builds.ApplyOptions{Runes: true, Items: true, Spells: true}
	if msg.WithRunes != nil {
		opt.Runes = *msg.WithRunes
	}
	if msg.WithItems != nil {
		opt.Items = *msg.WithItems
	}
	if msg.WithSpells != nil {
		opt.Spells = *msg.WithSpells
	}
	out, err := c.s.applyBuild(msg.buildRequest(), msg.BuildIndex, opt)
	if err != nil {
		c.respond(msg.ID, 502, nil, err.Error())
		return
	}
	status := 200
	if len(out.Done) == 0 {
		status = 500
	}
	c.respond(msg.ID, status, mustJSON(out), strings.Join(out.Errors, " · "))
}

func (s *Server) applyBuild(req builds.Request, index int, opt builds.ApplyOptions) (builds.ApplyResult, error) {
	cl := s.LCU.Client()
	if cl == nil {
		return builds.ApplyResult{}, errors.New("Client League non connecté")
	}
	if s.Builds == nil {
		return builds.ApplyResult{}, errors.New("builds indisponibles")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	res, err := s.Builds.Fetch(ctx, req)
	if err != nil {
		return builds.ApplyResult{}, err
	}
	if index < 0 || index >= len(res.Builds) {
		index = 0
	}
	res = s.finalItems(ctx, res)
	opt.InstallDir = cl.Credentials().InstallDir
	out := builds.Apply(ctx, cl, res, res.Builds[index], opt)
	log.Printf("[Builds] %s %s for champion %d: done=%v errors=%v item set=%s (install folder %q)",
		res.SourceName, res.Builds[index].Label, req.ChampionID, out.Done, out.Errors, out.ItemSet, opt.InstallDir)
	return out, nil
}

func (c *client) respond(id int64, status int, body json.RawMessage, errText string) {
	out := map[string]any{"type": "response", "id": id, "status": status}
	if body != nil {
		out["body"] = body
	}
	if errText != "" {
		out["error"] = errText
	}
	c.sendJSON(out)
}

func (c *client) handleRequest(msg inbound) {
	method := strings.ToUpper(msg.Method)
	if method == "" {
		method = http.MethodGet
	}
	if !allowed(method, msg.Path) {
		c.respond(msg.ID, 403, nil, "path not allowed: "+msg.Path)
		return
	}
	cl := c.s.LCU.Client()
	if cl == nil {
		c.respond(msg.ID, 503, nil, "Client League non connecté")
		return
	}
	var body []byte
	if len(msg.Body) > 0 && string(msg.Body) != "null" {
		body = msg.Body
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	start := time.Now()
	if method == http.MethodGet {
		if data, ok, err := c.s.static(ctx, cl, msg.Path); ok {
			if err != nil {
				log.Printf("[Phone] GET %s failed after %s: %v", msg.Path, time.Since(start).Round(time.Millisecond), err)
				c.respond(msg.ID, 502, nil, err.Error())
				return
			}
			c.respond(msg.ID, 200, data, "")
			return
		}
	}
	resp, err := cl.Do(ctx, method, msg.Path, body)
	took := time.Since(start).Round(time.Millisecond)
	if err != nil {
		log.Printf("[Phone] %s %s failed after %s: %v", method, msg.Path, took, err)
		c.respond(msg.ID, 502, nil, err.Error())
		return
	}
	// Failed or slow requests explain most "it does not work" reports.
	if resp.Status >= 400 && !(method == http.MethodGet && resp.Status == http.StatusNotFound) {
		log.Printf("[Phone] %s %s -> HTTP %d (%s)", method, msg.Path, resp.Status, took)
	} else if took > 3*time.Second {
		log.Printf("[Phone] %s %s slow: %s", method, msg.Path, took)
	}
	c.respond(msg.ID, resp.Status, resp.Body, "")
}

func (c *client) handleObserve(paths []string) {
	var fresh []string
	c.mu.Lock()
	for _, p := range paths {
		if !observable(p) {
			continue
		}
		dup := false
		for _, existing := range c.observed {
			if existing == p {
				dup = true
				break
			}
		}
		if !dup {
			c.observed = append(c.observed, p)
			fresh = append(fresh, p)
		}
	}
	c.mu.Unlock()
	go c.s.sendSnapshots(c, fresh)
}

func (c *client) handleUnobserve(paths []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.observed[:0]
	for _, existing := range c.observed {
		remove := false
		for _, p := range paths {
			if p == existing {
				remove = true
				break
			}
		}
		if !remove {
			kept = append(kept, existing)
		}
	}
	c.observed = kept
}

func (c *client) observes(uri string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.observed {
		if matches(p, uri) {
			return true
		}
	}
	return false
}

func (c *client) observedPaths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.observed...)
}

// sendSnapshots sends the current value of each exact path as an event, so
// the phone does not wait for the next change.
func (s *Server) sendSnapshots(c *client, paths []string) {
	cl := s.LCU.Client()
	if cl == nil {
		return
	}
	var wg sync.WaitGroup
	for _, p := range paths {
		if strings.HasSuffix(p, "*") {
			continue
		}
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			resp, err := cl.Do(ctx, http.MethodGet, p, nil)
			if err != nil {
				return
			}
			ev := map[string]any{"type": "event", "uri": p, "snapshot": true}
			if resp.Status >= 200 && resp.Status < 300 {
				ev["eventType"] = "Update"
				ev["data"] = resp.Body
			} else {
				ev["eventType"] = "Delete"
				ev["data"] = nil
			}
			c.sendJSON(ev)
		}(p)
	}
	wg.Wait()
}

func (c *client) handleSettings(msg inbound) {
	if msg.AutoBuild != nil || msg.BuildSource != nil {
		if err := c.s.Cfg.Update(func(cfg *config.Config) {
			if msg.AutoBuild != nil {
				cfg.AutoBuild = *msg.AutoBuild
			}
			if msg.BuildSource != nil && (*msg.BuildSource == "opgg" || *msg.BuildSource == "blitz") {
				cfg.BuildSource = *msg.BuildSource
			}
		}); err != nil {
			log.Printf("[Config] Could not save: %v", err)
		}
	}
	if msg.AutoAccept != nil {
		c.s.SetAutoAccept(*msg.AutoAccept)
	} else {
		c.s.broadcast(map[string]any{"type": "settings", "settings": c.s.settings()})
	}
	if msg.ID != 0 {
		c.respond(msg.ID, 200, mustJSON(c.s.settings()), "")
	}
}

// handleSelectSkin picks a skin in champion select. Through the plugin, the
// carousel moves to the skin, which is how Rose learns which skin to inject
// (and how the client selects an owned skin). Without the plugin, owned
// skins are still selected through the LCU API.
func (c *client) handleSelectSkin(msg inbound) {
	if connected, _ := c.s.Plugin.Connected(); connected {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		res, err := c.s.Plugin.Call(ctx, map[string]any{
			"type":       "select-skin",
			"skinId":     msg.SkinID,
			"championId": msg.ChampionID,
			"name":       msg.Name,
			"tilePath":   msg.TilePath,
			"index":      msg.Index,
			"owned":      msg.Owned,
		})
		if err == nil {
			var parsed struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			_ = json.Unmarshal(res, &parsed)
			if parsed.OK || !msg.Owned {
				status := 200
				if !parsed.OK {
					status = 409
				}
				c.respond(msg.ID, status, res, parsed.Error)
				return
			}
		} else if !msg.Owned {
			c.respond(msg.ID, 504, nil, "Le plugin n'a pas répondu: "+err.Error())
			return
		}
	}
	if !msg.Owned {
		c.respond(msg.ID, 409, nil, "Skin non possédé : il faut Rose avec le plugin LolMobil-Remote actif dans le client")
		return
	}
	c.patchSelectedSkin(msg.ID, msg.SkinID)
}

func (c *client) patchSelectedSkin(id int64, skinID int64) {
	cl := c.s.LCU.Client()
	if cl == nil {
		c.respond(id, 503, nil, "Client League non connecté")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body := mustJSON(map[string]any{"selectedSkinId": skinID})
	resp, err := cl.Do(ctx, http.MethodPatch, "/lol-champ-select/v1/session/my-selection", body)
	if err != nil {
		c.respond(id, 502, nil, err.Error())
		return
	}
	c.respond(id, resp.Status, resp.Body, "")
}

// handleSelectChroma forwards a chroma choice: to Rose (which injects
// unowned chromas) like its ChromaWheel does, and to the client for owned
// ones.
func (c *client) handleSelectChroma(msg inbound) {
	roseErr := error(nil)
	sentToRose := false
	if c.s.Rose.Connected() {
		roseErr = c.s.Rose.Send(map[string]any{
			"type":       "chroma-selection",
			"skinId":     msg.ChromaID,
			"chromaId":   msg.ChromaID,
			"chromaName": msg.Name,
			"championId": msg.ChampionID,
			"baseSkinId": msg.BaseSkinID,
			"timestamp":  time.Now().UnixMilli(),
		})
		sentToRose = roseErr == nil
	}
	if msg.Owned {
		c.patchSelectedSkin(msg.ID, msg.ChromaID)
		return
	}
	if sentToRose {
		c.respond(msg.ID, 200, mustJSON(map[string]any{"ok": true, "via": "rose"}), "")
		return
	}
	errText := "Chroma non possédé : Rose doit être lancé"
	if roseErr != nil {
		errText = roseErr.Error()
	}
	c.respond(msg.ID, 409, nil, errText)
}

// ---------------------------------------------------------------- fan-out

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

func (s *Server) snapshotClients() []*client {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		out = append(out, c)
	}
	return out
}

func (s *Server) broadcast(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	for _, c := range s.snapshotClients() {
		c.sendRaw(data)
	}
}

func (s *Server) settings() map[string]any {
	autoBuild, source := s.Cfg.BuildSettings()
	return map[string]any{"autoAccept": s.Cfg.GetAutoAccept(), "autoBuild": autoBuild, "buildSource": source}
}

func (s *Server) statusMessage() map[string]any {
	pluginOK, pluginVersion := s.Plugin.Connected()
	return map[string]any{
		"type":          "status",
		"lcu":           s.LCU.Client() != nil,
		"rose":          s.Rose.Connected(),
		"plugin":        pluginOK,
		"pluginVersion": pluginVersion,
		"relay":         s.RelayConnected != nil && s.RelayConnected(),
	}
}

// lanAddresses is LANAddresses (replaced in tests).
var lanAddresses = LANAddresses

func (s *Server) helloMessage(c *client) map[string]any {
	msg := map[string]any{
		"type":     "hello",
		"version":  s.Version,
		"name":     s.Name,
		"remote":   c.remote,
		"status":   s.statusMessage(),
		"settings": s.settings(),
		"rose":     s.Rose.Snapshot(),
	}
	if c.remote {
		// How to reach this PC on its Wi-Fi: the app connects directly when
		// the phone is on the same network (much faster than remote
		// access). The remote link already gives full access, so handing
		// it the Wi-Fi token gives nothing more.
		var urls []string
		for _, ip := range lanAddresses() {
			urls = append(urls, fmt.Sprintf("http://%s:%d", ip, s.Cfg.Port))
		}
		if len(urls) > 0 && s.Cfg.Token != "" {
			msg["lan"] = map[string]any{"urls": urls, "token": s.Cfg.Token}
		}
	}
	return msg
}

// Summary is what the tray icon shows.
type Summary struct {
	LCU, Rose, Plugin, Relay bool
	Phones                   int
}

// Summary returns the current connection state.
func (s *Server) Summary() Summary {
	pluginOK, _ := s.Plugin.Connected()
	s.mu.Lock()
	phones := len(s.clients)
	s.mu.Unlock()
	return Summary{
		LCU:    s.LCU.Client() != nil,
		Rose:   s.Rose.Connected(),
		Plugin: pluginOK,
		Relay:  s.RelayConnected != nil && s.RelayConnected(),
		Phones: phones,
	}
}

// SetAutoAccept changes the auto-accept option and tells the phones.
func (s *Server) SetAutoAccept(v bool) {
	if err := s.Cfg.Update(func(cfg *config.Config) { cfg.AutoAccept = v }); err != nil {
		log.Printf("[Config] Could not save: %v", err)
	}
	log.Printf("[Config] Auto-accept: %v", v)
	s.broadcast(map[string]any{"type": "settings", "settings": s.settings()})
}

// BroadcastStatus tells every phone what is connected.
func (s *Server) BroadcastStatus() {
	s.broadcast(s.statusMessage())
}

// OnLCUStatus is called when the League client connects or goes away.
func (s *Server) OnLCUStatus(cl *lcu.Client) {
	s.itemsMu.Lock()
	s.finalItemIDs = nil // new client, maybe a new patch
	s.itemsMu.Unlock()
	s.BroadcastStatus()
	if cl == nil {
		return
	}
	for _, c := range s.snapshotClients() {
		go s.sendSnapshots(c, c.observedPaths())
	}
}

// OnLCUEvent relays an LCU event to the phones following its URI.
func (s *Server) OnLCUEvent(ev lcu.Event) {
	if ev.URI == "/lol-matchmaking/v1/ready-check" {
		s.maybeAutoAccept(ev)
	}
	if ev.URI == "/lol-champ-select/v1/session" {
		s.maybeAutoBuild(ev)
	}
	var data []byte
	for _, c := range s.snapshotClients() {
		if !c.observes(ev.URI) {
			continue
		}
		if data == nil {
			var err error
			data, err = json.Marshal(map[string]any{
				"type":      "event",
				"uri":       ev.URI,
				"eventType": ev.EventType,
				"data":      ev.Data,
			})
			if err != nil {
				return
			}
		}
		c.sendRaw(data)
	}
}

func (s *Server) maybeAutoAccept(ev lcu.Event) {
	var rc struct {
		State          string `json:"state"`
		PlayerResponse string `json:"playerResponse"`
	}
	if ev.EventType == "Delete" || json.Unmarshal(ev.Data, &rc) != nil || rc.State != "InProgress" {
		s.readyMu.Lock()
		s.readyAccepted = false
		s.readyMu.Unlock()
		return
	}
	if rc.PlayerResponse != "None" || !s.Cfg.GetAutoAccept() {
		return
	}
	s.readyMu.Lock()
	if s.readyAccepted {
		s.readyMu.Unlock()
		return
	}
	s.readyAccepted = true
	s.readyMu.Unlock()
	go func() {
		cl := s.LCU.Client()
		if cl == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := cl.Do(ctx, http.MethodPost, "/lol-matchmaking/v1/ready-check/accept", nil); err != nil {
			log.Printf("[AutoAccept] Failed: %v", err)
			return
		}
		log.Printf("[AutoAccept] Match accepted")
		s.broadcast(map[string]any{"type": "notice", "text": "Partie acceptée automatiquement"})
	}()
}

// maybeAutoBuild applies the popular build once the player's champion is
// locked (option "autoBuild"), like the Blitz and OP.GG apps do.
func (s *Server) maybeAutoBuild(ev lcu.Event) {
	auto, source := s.Cfg.BuildSettings()
	if !auto || ev.EventType == "Delete" {
		return
	}
	var session struct {
		GameID  int64 `json:"gameId"`
		Local   int   `json:"localPlayerCellId"`
		Actions [][]struct {
			Actor     int    `json:"actorCellId"`
			Type      string `json:"type"`
			Completed bool   `json:"completed"`
		} `json:"actions"`
		MyTeam []struct {
			CellID   int    `json:"cellId"`
			Champion int    `json:"championId"`
			Position string `json:"assignedPosition"`
		} `json:"myTeam"`
	}
	if json.Unmarshal(ev.Data, &session) != nil {
		return
	}
	champion, position := 0, ""
	for _, c := range session.MyTeam {
		if c.CellID == session.Local {
			champion, position = c.Champion, c.Position
		}
	}
	if champion <= 0 {
		return
	}
	for _, group := range session.Actions {
		for _, a := range group {
			if a.Actor == session.Local && a.Type == "pick" && !a.Completed {
				return // not locked yet
			}
		}
	}
	key := fmt.Sprintf("%d/%d", session.GameID, champion)
	s.autoBuildMu.Lock()
	if s.autoBuildDone == key {
		s.autoBuildMu.Unlock()
		return
	}
	s.autoBuildDone = key
	s.autoBuildMu.Unlock()

	go func() {
		mode := "ranked"
		if cl := s.LCU.Client(); cl != nil {
			var flow struct {
				GameData struct {
					Queue struct {
						GameMode string `json:"gameMode"`
					} `json:"queue"`
				} `json:"gameData"`
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if resp, err := cl.Do(ctx, http.MethodGet, "/lol-gameflow/v1/session", nil); err == nil {
				_ = json.Unmarshal(resp.Body, &flow)
				mode = builds.ModeForQueue(flow.GameData.Queue.GameMode)
			}
			cancel()
		}
		req := builds.Request{Source: source, ChampionID: champion, Position: position, Mode: mode}
		out, err := s.applyBuild(req, 0, builds.ApplyOptions{Runes: true, Items: true, Spells: true})
		if err != nil {
			s.broadcast(map[string]any{"type": "notice", "text": "Build automatique impossible : " + err.Error()})
			return
		}
		text := "Build appliqué automatiquement : " + strings.Join(out.Done, ", ")
		if len(out.Errors) > 0 {
			text += " (" + strings.Join(out.Errors, " · ") + ")"
		}
		s.broadcast(map[string]any{"type": "notice", "text": text})
	}()
}

// OnRoseMessage relays a Rose broadcast.
func (s *Server) OnRoseMessage(_ string, raw json.RawMessage) {
	s.broadcast(map[string]any{"type": "rose", "msg": raw})
}

// OnPluginMessage relays an unsolicited plugin message.
func (s *Server) OnPluginMessage(raw json.RawMessage) {
	s.broadcast(map[string]any{"type": "plugin", "msg": raw})
}
