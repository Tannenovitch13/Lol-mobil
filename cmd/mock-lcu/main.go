// mock-lcu imitates the League client API (REST + WAMP events) closely
// enough to develop and test LolMobil without the game. It plays a full
// flow: lobby -> queue -> ready check -> bans/picks -> finalization -> game.
// With -rose it also imitates Rose's bridge on 127.0.0.1:50000.
//
// Jump to a state with: GET /mock/goto?state=lobby|matchmaking|readycheck|champselect|finalization|aram|ingame|endofgame|none
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	port     = flag.Int("port", 29999, "port")
	password = flag.String("password", "test", "basic auth password (user riot)")
	withRose = flag.Bool("rose", false, "also imitate Rose's bridge on 127.0.0.1:50000")
	speed    = flag.Duration("step", 1500*time.Millisecond, "delay of automatic steps (queue pop, other players' picks)")
)

type mock struct {
	mu        sync.Mutex
	phase     string
	queueID   int
	lobby     map[string]any
	search    map[string]any
	ready     map[string]any
	session   map[string]any
	myChamp   int
	skinID    int
	spell1    int
	spell2    int
	pages     []map[string]any
	currentPg int
	nextPage  int
	gen       int // invalidates pending automatic steps

	subsMu sync.Mutex
	subs   map[*websocket.Conn]*sync.Mutex
}

func main() {
	flag.Parse()
	m := &mock{phase: "None", spell1: 4, spell2: 14, subs: map[*websocket.Conn]*sync.Mutex{}}
	m.initPages()
	if *withRose {
		go runRose()
	}
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	log.Printf("mock LCU on http://%s (user riot, password %s)", addr, *password)
	log.Fatal(http.ListenAndServe(addr, m))
}

// ------------------------------------------------------------ helpers

func (m *mock) emit(uri string, data any) {
	eventType := "Update"
	if data == nil {
		eventType = "Delete"
	}
	frame, _ := json.Marshal([]any{8, "OnJsonApiEvent", map[string]any{"uri": uri, "eventType": eventType, "data": data}})
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for conn, mu := range m.subs {
		mu.Lock()
		_ = conn.WriteMessage(websocket.TextMessage, frame)
		mu.Unlock()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter, msg string) {
	writeJSON(w, 404, map[string]any{"errorCode": "RPC_ERROR", "httpStatus": 404, "message": msg})
}

func (m *mock) later(d time.Duration, fn func()) {
	gen := m.gen
	time.AfterFunc(d, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.gen == gen {
			fn()
		}
	})
}

func clone(v any) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

// ------------------------------------------------------------ state changes (m.mu held)

func (m *mock) setPhase(p string) {
	m.phase = p
	m.gen++
	m.emit("/lol-gameflow/v1/gameflow-phase", p)
	m.emit("/lol-gameflow/v1/session", m.gameflowSession())
}

func (m *mock) gameflowSession() map[string]any {
	q := map[string]any{"id": m.queueID, "gameMode": "CLASSIC", "description": ""}
	mapID := 11
	for _, qq := range queues() {
		if qq["id"] == m.queueID {
			q = qq
			if id, ok := qq["mapId"].(int); ok {
				mapID = id
			}
		}
	}
	return map[string]any{"phase": m.phase, "gameData": map[string]any{"queue": q}, "map": map[string]any{"id": mapID, "gameMode": q["gameMode"]}}
}

func (m *mock) makeLobby(queueID int) {
	m.queueID = queueID
	positions := queueID == 400 || queueID == 420 || queueID == 440
	me := map[string]any{
		"puuid": "me", "summonerId": 1, "gameName": "Testeur", "tagLine": "EUW", "summonerIconId": 29,
		"isLeader": true, "firstPositionPreference": "MIDDLE", "secondPositionPreference": "TOP",
	}
	friend := map[string]any{
		"puuid": "friend", "summonerId": 2, "gameName": "Coéquipier", "tagLine": "EUW", "summonerIconId": 7,
		"isLeader": false, "firstPositionPreference": "UTILITY", "secondPositionPreference": "BOTTOM",
	}
	m.lobby = map[string]any{
		"canStartActivity": true,
		"gameConfig":       map[string]any{"queueId": queueID, "showPositionSelector": positions, "gameMode": "CLASSIC"},
		"localMember":      me,
		"members":          []any{me, friend},
		"invitations":      []any{},
	}
	m.emit("/lol-lobby/v2/lobby", m.lobby)
	if m.phase != "Lobby" {
		m.setPhase("Lobby")
	}
}

func (m *mock) startSearch() {
	m.search = map[string]any{"searchState": "Searching", "timeInQueue": 0.0, "estimatedQueueTime": 95.0, "errors": []any{}}
	m.emit("/lol-matchmaking/v1/search", m.search)
	m.setPhase("Matchmaking")
	m.later(*speed*2, m.popQueue)
}

func (m *mock) popQueue() {
	m.ready = map[string]any{"state": "InProgress", "playerResponse": "None", "timer": 0, "declinerIds": []any{}}
	m.emit("/lol-matchmaking/v1/ready-check", m.ready)
	m.setPhase("ReadyCheck")
}

func (m *mock) cell(id int, champ int, pos string, name string) map[string]any {
	return map[string]any{
		"cellId": id, "championId": champ, "championPickIntent": 0, "assignedPosition": pos,
		"gameName": name, "selectedSkinId": 0, "spell1Id": 4, "spell2Id": 7, "team": 1 + id/5,
	}
}

func (m *mock) startChampSelect(aram bool) {
	m.myChamp = 0
	m.skinID = 0
	positions := []string{"top", "jungle", "middle", "bottom", "utility"}
	names := []string{"Coéquipier", "JungleKing", "", "BotLaner", "Supp0rt"}
	var myTeam, theirTeam []any
	for i := 0; i < 5; i++ {
		c := m.cell(i, 0, positions[i], names[i])
		if i == 2 {
			c["spell1Id"], c["spell2Id"] = m.spell1, m.spell2
		}
		myTeam = append(myTeam, c)
		theirTeam = append(theirTeam, m.cell(5+i, 0, "", ""))
	}
	m.session = map[string]any{
		"localPlayerCellId": 2, "myTeam": myTeam, "theirTeam": theirTeam,
		"bans":   map[string]any{"myTeamBans": []any{}, "theirTeamBans": []any{}, "numBans": 6},
		"trades": []any{}, "pickOrderSwaps": []any{}, "positionSwaps": []any{},
		"benchEnabled": false, "benchChampions": []any{}, "allowRerolling": false, "rerollsRemaining": 0,
		"timer": map[string]any{"phase": "BAN_PICK", "adjustedTimeLeftInPhase": 30000, "isInfinite": false, "totalTimeInPhase": 30000},
	}
	if aram {
		m.myChamp = 103
		for i, c := range myTeam {
			c.(map[string]any)["championId"] = champions[i*2].ID
		}
		myTeam[2].(map[string]any)["championId"] = m.myChamp
		m.session["benchEnabled"] = true
		m.session["benchChampions"] = []any{map[string]any{"championId": 99}, map[string]any{"championId": 222}}
		m.session["allowRerolling"] = true
		m.session["rerollsRemaining"] = 2
		m.session["actions"] = []any{}
		m.session["theirTeam"] = []any{}
		m.session["timer"] = map[string]any{"phase": "FINALIZATION", "adjustedTimeLeftInPhase": 60000, "totalTimeInPhase": 60000}
	} else {
		// Simultaneous bans, then picks: ally top, 2 enemies, me + jungle, ...
		var bans []any
		for i := 0; i < 10; i++ {
			bans = append(bans, map[string]any{"id": i + 1, "actorCellId": i, "championId": 0, "completed": false, "isAllyAction": i < 5, "isInProgress": true, "type": "ban"})
		}
		pick := func(id, cell int) map[string]any {
			return map[string]any{"id": id, "actorCellId": cell, "championId": 0, "completed": false, "isAllyAction": cell < 5, "isInProgress": false, "type": "pick"}
		}
		m.session["actions"] = []any{bans, []any{pick(11, 0)}, []any{pick(12, 5), pick(13, 6)}, []any{pick(14, 2), pick(15, 1)}, []any{pick(16, 7), pick(17, 8)}, []any{pick(18, 3), pick(19, 4)}, []any{pick(20, 9)}}
		// Other players ban at once.
		taken := map[int]bool{}
		for _, a := range bans {
			act := a.(map[string]any)
			if act["actorCellId"] != 2 {
				act["championId"] = randomChamp(taken)
				act["completed"] = true
				act["isInProgress"] = false
			}
		}
	}
	m.emit("/lol-champ-select/v1/session", m.session)
	m.emit("/lol-champ-select/v1/skin-carousel-skins", m.skins())
	m.setPhase("ChampSelect")
}

func randomChamp(taken map[int]bool) int {
	for {
		c := champions[rand.Intn(len(champions))].ID
		if !taken[c] && c != 103 {
			taken[c] = true
			return c
		}
	}
}

func (m *mock) skins() any {
	if m.myChamp == 0 {
		return []any{}
	}
	return carouselSkins(m.myChamp)
}

func (m *mock) myCell() map[string]any {
	for _, c := range m.session["myTeam"].([]any) {
		cell := c.(map[string]any)
		if cell["cellId"] == 2 {
			return cell
		}
	}
	return nil
}

func (m *mock) allActions() []map[string]any {
	var out []map[string]any
	for _, group := range m.session["actions"].([]any) {
		for _, a := range group.([]any) {
			out = append(out, a.(map[string]any))
		}
	}
	return out
}

func (m *mock) takenChamps() map[int]bool {
	taken := map[int]bool{}
	for _, a := range m.allActions() {
		if id, ok := a["championId"].(int); ok && id > 0 && a["completed"] == true {
			taken[id] = true
		}
	}
	return taken
}

// advance moves the draft forward: completes groups and makes the next
// group active. Other players pick automatically after a delay.
func (m *mock) advance() {
	groups := m.session["actions"].([]any)
	for gi, g := range groups {
		group := g.([]any)
		done := true
		for _, a := range group {
			if a.(map[string]any)["completed"] != true {
				done = false
			}
		}
		if done {
			continue
		}
		mineWaiting := false
		for _, a := range group {
			act := a.(map[string]any)
			if act["completed"] == true {
				continue
			}
			act["isInProgress"] = true
			if act["actorCellId"] == 2 {
				mineWaiting = true
			}
		}
		if gi == 0 {
			// ban phase: wait for me
		} else {
			m.later(*speed, func() {
				taken := m.takenChamps()
				for _, a := range group {
					act := a.(map[string]any)
					if act["completed"] == true || act["actorCellId"] == 2 {
						continue
					}
					champID := randomChamp(taken)
					act["championId"] = champID
					act["completed"] = true
					act["isInProgress"] = false
					m.setCellChamp(act["actorCellId"].(int), champID)
				}
				if !mineWaiting {
					m.advance()
				} else {
					m.emit("/lol-champ-select/v1/session", m.session)
				}
			})
		}
		m.session["timer"] = map[string]any{"phase": "BAN_PICK", "adjustedTimeLeftInPhase": 30000, "totalTimeInPhase": 30000}
		m.emit("/lol-champ-select/v1/session", m.session)
		return
	}
	// Everything picked.
	m.session["timer"] = map[string]any{"phase": "FINALIZATION", "adjustedTimeLeftInPhase": 60000, "totalTimeInPhase": 60000}
	m.session["trades"] = []any{map[string]any{"id": 1, "cellId": 1, "state": "AVAILABLE"}}
	m.emit("/lol-champ-select/v1/session", m.session)
}

func (m *mock) setCellChamp(cellID, champID int) {
	for _, key := range []string{"myTeam", "theirTeam"} {
		for _, c := range m.session[key].([]any) {
			cell := c.(map[string]any)
			if cell["cellId"] == cellID {
				cell["championId"] = champID
			}
		}
	}
	if cellID == 2 {
		m.myChamp = champID
		m.emit("/lol-champ-select/v1/skin-carousel-skins", m.skins())
	}
}

func (m *mock) initPages() {
	page := func(id int, name string, editable bool, primary, sub int, perks []int) map[string]any {
		return map[string]any{"id": id, "name": name, "isEditable": editable, "isDeletable": editable, "isValid": true,
			"isActive": false, "primaryStyleId": primary, "subStyleId": sub, "selectedPerkIds": perks, "current": false, "order": id}
	}
	m.pages = []map[string]any{
		page(1, "Domination (défaut)", false, 8100, 8300, []int{8112, 8126, 8140, 8135, 8347, 8304, 5008, 5008, 5011}),
		page(50, "Mid AP", true, 8200, 8300, []int{8229, 8226, 8210, 8237, 8347, 8304, 5008, 5008, 5011}),
		page(51, "Bruiser", true, 8000, 8400, []int{8010, 9111, 9104, 8299, 8444, 8451, 5005, 5008, 5011}),
	}
	m.currentPg = 50
	m.nextPage = 52
	m.pages[1]["current"] = true
}

func (m *mock) currentPage() map[string]any {
	for _, p := range m.pages {
		if p["id"] == m.currentPg {
			return p
		}
	}
	return nil
}

func (m *mock) emitPages() {
	for _, p := range m.pages {
		p["current"] = p["id"] == m.currentPg
		p["isActive"] = p["current"]
	}
	m.emit("/lol-perks/v1/pages", m.pages)
	m.emit("/lol-perks/v1/currentpage", m.currentPage())
}

func (m *mock) goTo(state string) {
	switch state {
	case "none":
		m.lobby = nil
		m.emit("/lol-lobby/v2/lobby", nil)
		m.setPhase("None")
	case "lobby":
		m.makeLobby(420)
	case "matchmaking":
		m.makeLobby(420)
		m.startSearch()
	case "readycheck":
		m.makeLobby(420)
		m.popQueue()
	case "champselect":
		m.makeLobby(420)
		m.startChampSelect(false)
	case "finalization":
		m.makeLobby(420)
		m.startChampSelect(false)
		taken := m.takenChamps()
		for _, a := range m.allActions() {
			id := 103
			if a["actorCellId"] != 2 {
				id = randomChamp(taken)
			}
			a["championId"] = id
			a["completed"] = true
			a["isInProgress"] = false
			if a["type"] == "pick" {
				m.setCellChamp(a["actorCellId"].(int), id)
			}
		}
		m.gen++
		m.advance()
	case "aram":
		m.makeLobby(450)
		m.startChampSelect(true)
	case "ingame":
		m.setPhase("InProgress")
	case "endofgame":
		m.setPhase("EndOfGame")
	}
}

// ------------------------------------------------------------ HTTP

var (
	reAction      = regexp.MustCompile(`^/lol-champ-select/v1/session/actions/(\d+)(/complete)?$`)
	reBenchSwap   = regexp.MustCompile(`^/lol-champ-select/v1/session/bench/swap/(\d+)$`)
	rePage        = regexp.MustCompile(`^/lol-perks/v1/pages/(\d+)$`)
	reRecommended = regexp.MustCompile(`^/lol-perks/v1/recommended-pages/champion/(\d+)/`)
	reMember      = regexp.MustCompile(`^/lol-lobby/v2/lobby/members/(\d+)/(kick|promote|grant-invite|revoke-invite)$`)
	reTrade       = regexp.MustCompile(`^/lol-champ-select/v1/session/(trades|swaps|position-swaps)/(\d+)/(request|accept|decline)$`)
)

func (m *mock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/mock/goto") {
		m.mu.Lock()
		m.goTo(r.URL.Query().Get("state"))
		m.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if m.serveFakeClient(w, r) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/lol-game-data/assets/") && !strings.HasSuffix(r.URL.Path, ".json") {
		serveImage(w, r.URL.Path) // no auth: lets the fake client page show thumbnails
		return
	}
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("riot:"+*password))
	if r.Header.Get("Authorization") != expected {
		writeJSON(w, 401, map[string]any{"message": "unauthorized"})
		return
	}
	if r.URL.Path == "/" && websocket.IsWebSocketUpgrade(r) {
		m.serveWAMP(w, r)
		return
	}
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)

	m.mu.Lock()
	defer m.mu.Unlock()
	p := r.URL.Path
	key := r.Method + " " + p
	switch key {
	case "GET /lol-gameflow/v1/gameflow-phase":
		writeJSON(w, 200, m.phase)
	case "GET /lol-gameflow/v1/session":
		writeJSON(w, 200, m.gameflowSession())
	case "GET /lol-summoner/v1/current-summoner":
		writeJSON(w, 200, map[string]any{"gameName": "Testeur", "tagLine": "EUW", "displayName": "Testeur", "profileIconId": 29, "summonerLevel": 187})
	case "GET /lol-lobby/v2/received-invitations":
		writeJSON(w, 200, []any{})
	case "GET /lol-chat/v1/friends":
		writeJSON(w, 200, mockFriends())
	case "GET /lol-suggested-players/v1/suggested-players":
		writeJSON(w, 200, []any{map[string]any{"summonerId": 30, "gameName": "Récent1", "summonerIconId": 12, "reason": "RECENT_GAMES"}})
	case "GET /lol-summoner/v1/summoners":
		name := r.URL.Query().Get("name")
		for _, f := range mockFriends() {
			if strings.EqualFold(f["gameName"].(string)+"#"+f["gameTag"].(string), name) {
				writeJSON(w, 200, map[string]any{"summonerId": f["summonerId"], "gameName": f["gameName"], "tagLine": f["gameTag"]})
				return
			}
		}
		notFound(w, "summoner not found")
	case "POST /lol-lobby/v2/lobby/invitations":
		if m.lobby == nil {
			notFound(w, "no lobby")
			return
		}
		var invites []map[string]any
		_ = json.Unmarshal(raw, &invites)
		for _, inv := range invites {
			id := int(inv["toSummonerId"].(float64))
			m.lobby["invitations"] = append(m.lobby["invitations"].([]any), map[string]any{
				"invitationId": fmt.Sprintf("inv-%d", id), "toSummonerId": id, "state": "Pending",
			})
			// The friend accepts after a moment and joins.
			m.later(*speed, func() {
				if m.lobby == nil {
					return
				}
				for _, it := range m.lobby["invitations"].([]any) {
					inv := it.(map[string]any)
					if inv["toSummonerId"] == id {
						inv["state"] = "Accepted"
					}
				}
				for _, f := range mockFriends() {
					if f["summonerId"] == id {
						m.lobby["members"] = append(m.lobby["members"].([]any), map[string]any{
							"puuid": f["puuid"], "summonerId": id, "gameName": f["gameName"], "tagLine": f["gameTag"],
							"summonerIconId": f["icon"], "isLeader": false, "firstPositionPreference": "FILL",
						})
					}
				}
				m.emit("/lol-lobby/v2/lobby", m.lobby)
			})
		}
		m.emit("/lol-lobby/v2/lobby", m.lobby)
		writeJSON(w, 200, invites)
	case "GET /lol-lobby/v2/lobby":
		if m.lobby == nil {
			notFound(w, "LOBBY_NOT_FOUND")
			return
		}
		writeJSON(w, 200, m.lobby)
	case "POST /lol-lobby/v2/lobby":
		q, _ := body["queueId"].(float64)
		m.makeLobby(int(q))
		writeJSON(w, 200, m.lobby)
	case "DELETE /lol-lobby/v2/lobby":
		m.lobby = nil
		m.emit("/lol-lobby/v2/lobby", nil)
		m.setPhase("None")
		w.WriteHeader(204)
	case "PUT /lol-lobby/v2/lobby/members/localMember/position-preferences":
		if m.lobby != nil {
			lm := m.lobby["localMember"].(map[string]any)
			lm["firstPositionPreference"] = body["firstPreference"]
			lm["secondPositionPreference"] = body["secondPreference"]
			m.emit("/lol-lobby/v2/lobby", m.lobby)
		}
		w.WriteHeader(204)
	case "POST /lol-lobby/v2/lobby/matchmaking/search":
		m.startSearch()
		w.WriteHeader(204)
	case "DELETE /lol-lobby/v2/lobby/matchmaking/search":
		m.search = map[string]any{"searchState": "Invalid", "errors": []any{}}
		m.emit("/lol-matchmaking/v1/search", m.search)
		m.setPhase("Lobby")
		w.WriteHeader(204)
	case "GET /lol-matchmaking/v1/search":
		if m.search == nil {
			notFound(w, "no search")
			return
		}
		writeJSON(w, 200, m.search)
	case "GET /lol-matchmaking/v1/ready-check":
		if m.phase != "ReadyCheck" {
			notFound(w, "no ready check")
			return
		}
		writeJSON(w, 200, m.ready)
	case "POST /lol-matchmaking/v1/ready-check/accept":
		m.ready["playerResponse"] = "Accepted"
		m.emit("/lol-matchmaking/v1/ready-check", m.ready)
		m.later(*speed, func() {
			m.emit("/lol-matchmaking/v1/ready-check", nil)
			m.startChampSelect(m.queueID == 450)
			if m.queueID != 450 {
				m.advance()
			}
		})
		w.WriteHeader(204)
	case "POST /lol-matchmaking/v1/ready-check/decline":
		m.ready["playerResponse"] = "Declined"
		m.emit("/lol-matchmaking/v1/ready-check", m.ready)
		m.later(*speed, func() { m.setPhase("Lobby") })
		w.WriteHeader(204)
	case "GET /lol-champ-select/v1/session":
		if m.phase != "ChampSelect" {
			notFound(w, "No active delegate")
			return
		}
		writeJSON(w, 200, m.session)
	case "GET /lol-champ-select/v1/pickable-champion-ids", "GET /lol-champ-select/v1/bannable-champion-ids":
		if m.phase != "ChampSelect" {
			notFound(w, "No active delegate")
			return
		}
		taken := m.takenChamps()
		var ids []int
		for _, c := range champions {
			if !taken[c.ID] {
				ids = append(ids, c.ID)
			}
		}
		writeJSON(w, 200, ids)
	case "GET /lol-champ-select/v1/skin-carousel-skins":
		if m.phase != "ChampSelect" {
			notFound(w, "No active delegate")
			return
		}
		writeJSON(w, 200, m.skins())
	case "PATCH /lol-champ-select/v1/session/my-selection":
		cell := m.myCell()
		if v, ok := body["selectedSkinId"].(float64); ok {
			owned := false
			for _, s := range carouselSkins(m.myChamp) {
				if s["id"] == int(v) && s["unlocked"] == true {
					owned = true
				}
			}
			if !owned {
				writeJSON(w, 400, map[string]any{"message": "Skin not owned"})
				return
			}
			cell["selectedSkinId"] = int(v)
		}
		if v, ok := body["spell1Id"].(float64); ok {
			cell["spell1Id"] = int(v)
			m.spell1 = int(v)
		}
		if v, ok := body["spell2Id"].(float64); ok {
			cell["spell2Id"] = int(v)
			m.spell2 = int(v)
		}
		m.emit("/lol-champ-select/v1/session", m.session)
		w.WriteHeader(204)
	case "POST /lol-champ-select/v1/session/my-selection/reroll":
		if n, _ := m.session["rerollsRemaining"].(int); n > 0 {
			m.session["rerollsRemaining"] = n - 1
			m.setCellChamp(2, randomChamp(map[int]bool{m.myChamp: true}))
			m.emit("/lol-champ-select/v1/session", m.session)
		}
		w.WriteHeader(204)
	case "GET /lol-game-data/assets/v1/champion-summary.json":
		writeJSON(w, 200, championSummary())
	case "GET /lol-game-data/assets/v1/summoner-spells.json":
		writeJSON(w, 200, spells())
	case "GET /lol-game-queues/v1/queues":
		writeJSON(w, 200, queues())
	case "GET /lol-perks/v1/styles":
		writeJSON(w, 200, stylesJSON())
	case "GET /lol-perks/v1/perks":
		writeJSON(w, 200, perksJSON())
	case "GET /lol-perks/v1/pages":
		writeJSON(w, 200, m.pages)
	case "GET /lol-perks/v1/currentpage":
		writeJSON(w, 200, m.currentPage())
	case "PUT /lol-perks/v1/currentpage":
		var id int
		_ = json.Unmarshal(raw, &id)
		m.currentPg = id
		m.emitPages()
		w.WriteHeader(204)
	case "POST /lol-perks/v1/pages":
		if len(m.pages) >= 5 {
			writeJSON(w, 400, map[string]any{"message": "Max pages reached"})
			return
		}
		body["id"] = m.nextPage
		body["isEditable"], body["isDeletable"], body["isValid"] = true, true, true
		m.nextPage++
		m.pages = append(m.pages, body)
		m.currentPg = body["id"].(int)
		m.emitPages()
		writeJSON(w, 200, body)
	case "POST /lol-gameflow/v1/reconnect", "POST /lol-lobby/v2/play-again":
		if p == "/lol-lobby/v2/play-again" {
			m.makeLobby(m.queueID)
		}
		w.WriteHeader(204)
	default:
		m.serveDynamic(w, r, body, raw)
	}
}

func (m *mock) serveDynamic(w http.ResponseWriter, r *http.Request, body map[string]any, raw []byte) {
	p := r.URL.Path
	if mt := reAction.FindStringSubmatch(p); mt != nil && m.session != nil {
		id, _ := strconv.Atoi(mt[1])
		for _, a := range m.allActions() {
			if a["id"] != id {
				continue
			}
			if a["actorCellId"] != 2 {
				writeJSON(w, 400, map[string]any{"message": "not your action"})
				return
			}
			if v, ok := body["championId"].(float64); ok && r.Method == http.MethodPatch {
				a["championId"] = int(v)
				if a["type"] == "pick" && a["isInProgress"] != true {
					m.myCell()["championPickIntent"] = int(v)
				}
			}
			complete := mt[2] != "" || body["completed"] == true
			if complete {
				if a["isInProgress"] != true || a["championId"].(int) <= 0 {
					writeJSON(w, 400, map[string]any{"message": "cannot complete"})
					return
				}
				a["completed"] = true
				a["isInProgress"] = false
				if a["type"] == "pick" {
					m.setCellChamp(2, a["championId"].(int))
				}
				if a["type"] == "ban" {
					bans := m.session["bans"].(map[string]any)
					bans["myTeamBans"] = append(bans["myTeamBans"].([]any), a["championId"])
				}
				m.advance()
			} else {
				m.emit("/lol-champ-select/v1/session", m.session)
			}
			w.WriteHeader(204)
			return
		}
		notFound(w, "action not found")
		return
	}
	if mt := reBenchSwap.FindStringSubmatch(p); mt != nil && m.session != nil {
		id, _ := strconv.Atoi(mt[1])
		bench := m.session["benchChampions"].([]any)
		for i, b := range bench {
			if b.(map[string]any)["championId"] == id {
				bench[i] = map[string]any{"championId": m.myChamp}
				m.setCellChamp(2, id)
				m.emit("/lol-champ-select/v1/session", m.session)
				w.WriteHeader(204)
				return
			}
		}
		notFound(w, "not on bench")
		return
	}
	if mt := rePage.FindStringSubmatch(p); mt != nil {
		id, _ := strconv.Atoi(mt[1])
		for i, page := range m.pages {
			if page["id"] != id {
				continue
			}
			switch r.Method {
			case http.MethodPut:
				if page["isEditable"] != true {
					writeJSON(w, 400, map[string]any{"message": "page not editable"})
					return
				}
				body["id"] = id
				body["isEditable"], body["isDeletable"], body["isValid"] = true, true, true
				m.pages[i] = body
				if body["current"] == true {
					m.currentPg = id
				}
				m.emitPages()
				writeJSON(w, 200, body)
			case http.MethodDelete:
				m.pages = append(m.pages[:i], m.pages[i+1:]...)
				m.emitPages()
				w.WriteHeader(204)
			default:
				writeJSON(w, 200, page)
			}
			return
		}
		notFound(w, "page not found")
		return
	}
	if mt := reRecommended.FindStringSubmatch(p); mt != nil {
		writeJSON(w, 200, []any{
			map[string]any{"position": "MIDDLE", "primaryPerkStyleId": 8100, "secondaryPerkStyleId": 8200, "keystone": map[string]any{"id": 8112},
				"perks": perkObjs(8112, 8126, 8140, 8106, 8226, 8210, 5008, 5008, 5001)},
			map[string]any{"position": "MIDDLE", "primaryPerkStyleId": 8200, "secondaryPerkStyleId": 8300, "keystone": map[string]any{"id": 8229},
				"perks": perkObjs(8229, 8226, 8210, 8237, 8347, 8304, 5008, 5008, 5001)},
		})
		return
	}
	if mt := reMember.FindStringSubmatch(p); mt != nil && m.lobby != nil && r.Method == http.MethodPost {
		id, _ := strconv.Atoi(mt[1])
		members := m.lobby["members"].([]any)
		for i, it := range members {
			member := it.(map[string]any)
			if member["summonerId"] != id {
				continue
			}
			switch mt[2] {
			case "kick":
				m.lobby["members"] = append(members[:i:i], members[i+1:]...)
			case "promote":
				for _, other := range members {
					other.(map[string]any)["isLeader"] = false
				}
				member["isLeader"] = true
			case "grant-invite":
				member["allowedInviteOthers"] = true
			case "revoke-invite":
				member["allowedInviteOthers"] = false
			}
			m.emit("/lol-lobby/v2/lobby", m.lobby)
			w.WriteHeader(204)
			return
		}
		notFound(w, "member not found")
		return
	}
	if mt := reTrade.FindStringSubmatch(p); mt != nil {
		w.WriteHeader(204)
		return
	}
	notFound(w, "mock: unknown "+r.Method+" "+p)
}

func mockFriends() []map[string]any {
	f := func(id int, name, availability, status string) map[string]any {
		return map[string]any{
			"summonerId": id, "puuid": fmt.Sprintf("p%d", id), "gameName": name, "gameTag": "EUW", "name": name,
			"availability": availability, "icon": id, "lol": map[string]any{"gameStatus": status},
		}
	}
	return []map[string]any{
		f(20, "Ami Dispo", "chat", "outOfGame"),
		f(21, "Ami EnPartie", "dnd", "inGame"),
		f(22, "Ami Absent", "away", "outOfGame"),
		f(23, "Ami HorsLigne", "offline", ""),
	}
}

func perkObjs(ids ...int) []any {
	var out []any
	for _, id := range ids {
		out = append(out, map[string]any{"id": id})
	}
	return out
}

func (m *mock) serveWAMP(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{Subprotocols: []string{"wamp"}, CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if strings.HasPrefix(string(msg), "[5") {
			m.subsMu.Lock()
			m.subs[conn] = &sync.Mutex{}
			m.subsMu.Unlock()
		}
	}
	m.subsMu.Lock()
	delete(m.subs, conn)
	m.subsMu.Unlock()
}

// serveImage draws a placeholder picture whose colour depends on the path.
func serveImage(w http.ResponseWriter, path string) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	sum := h.Sum32()
	base := color.RGBA{uint8(sum), uint8(sum >> 8), uint8(sum >> 16), 255}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			f := float64(x+y) / 128
			img.Set(x, y, color.RGBA{uint8(float64(base.R) * (1 - f/2)), uint8(float64(base.G) * (1 - f/2)), uint8(float64(base.B)*(1-f/2) + 40*f), 255})
		}
	}
	w.Header().Set("Content-Type", "image/png")
	_ = png.Encode(w, img)
}
