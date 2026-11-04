package builds

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tannenovitch13/lol-mobil/internal/lcu"
)

func TestOPGGParse(t *testing.T) {
	fixture, err := os.ReadFile("testdata/opgg_ahri_mid.json")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	s := NewService(srv.URL, "")
	res, err := s.Fetch(context.Background(), Request{Source: "opgg", ChampionID: 103, Position: "middle"})
	if err != nil {
		t.Fatal(err)
	}
	if paths[0] != "/api/global/champions/ranked/103/mid?tier=emerald_plus" {
		t.Errorf("unexpected URL %s", paths[0])
	}
	if res.Position != "middle" || res.Patch != "16.22" || len(res.Builds) != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}
	b := res.Builds[0]
	if !b.Runes.Valid() || b.Runes.Perks[0] != 8112 || b.Runes.Perks[8] != 5001 {
		t.Errorf("runes: %+v", b.Runes)
	}
	if b.RuneStats.WinRate < 0.51 || b.RuneStats.WinRate > 0.53 {
		t.Errorf("rune win rate %v", b.RuneStats.WinRate)
	}
	if len(b.Spells) != 2 || b.Core[0] != 6655 || b.Boots[0] != 3020 || b.Starter[0] != 1056 {
		t.Errorf("items/spells: %+v", b)
	}
	if b.CoreStats.WinRate < 0.54 || b.CoreStats.WinRate > 0.56 || b.StarterStats.WinRate < 0.51 || b.BootsStats.WinRate < 0.51 {
		t.Errorf("item stats: core %+v starter %+v boots %+v", b.CoreStats, b.StarterStats, b.BootsStats)
	}
	for _, id := range b.Situational {
		if id == 3089 {
			t.Errorf("core item repeated in situational: %v", b.Situational)
		}
	}
	if strings.Join(b.SkillOrder, "") != "QWE" {
		t.Errorf("skill order %v", b.SkillOrder)
	}
	if res.Builds[1].Core[0] != 3165 {
		t.Errorf("variant should use the 2nd core build")
	}

	// Cached: no second request.
	if _, err := s.Fetch(context.Background(), Request{Source: "opgg", ChampionID: 103, Position: "middle"}); err != nil || len(paths) != 1 {
		t.Errorf("cache not used: %d requests", len(paths))
	}
}

func TestOPGGPicksMainRole(t *testing.T) {
	fixture, _ := os.ReadFile("testdata/opgg_ahri_mid.json")
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()
	res, err := NewService(srv.URL, "").Fetch(context.Background(), Request{Source: "opgg", ChampionID: 103})
	if err != nil {
		t.Fatal(err)
	}
	if res.Position != "middle" || len(paths) != 1 {
		t.Errorf("main role: %s after %v", res.Position, paths)
	}
}

func TestBlitzParse(t *testing.T) {
	row := []any{
		"MIDDLE", "16.22", 0.4,
		`[{"perks":[8112,8126,8140,8135,8226,8210,5008,5008,5001],"games":1000,"wins":520,"pick_rate":0.5}]`,
		`[{"summonerSpellIds":[4,14],"games":900,"wins":470,"pick_rate":0.6}]`,
		`[{"itemIds":[1056,2003,2003],"games":800,"wins":410,"pick_rate":0.7}]`,
		`[{"itemIds":[6655,4645,3089],"games":500,"wins":270,"pick_rate":0.3}]`,
		`[{"itemId":3020,"games":400,"wins":200},{"itemId":3157,"games":300,"wins":160}]`,
		`[{"skillOrder":[1,2,3,1,1,4,1,2,1,2,4,2,2,3,3,4,3,3]}]`,
	}
	payload := map[string]any{
		"manifest": map[string]any{"schema": map[string]any{"columns": []map[string]any{
			{"name": "individual_position", "position": 0, "typeName": "STRING"},
			{"name": "patch", "position": 1, "typeName": "STRING"},
			{"name": "pick_rate", "position": 2, "typeName": "DOUBLE"},
			{"name": "runes", "position": 3, "typeName": "ARRAY"},
			{"name": "summonerSpells", "position": 4, "typeName": "ARRAY"},
			{"name": "startingItems", "position": 5, "typeName": "ARRAY"},
			{"name": "coreItems", "position": 6, "typeName": "ARRAY"},
			{"name": "situationalItems", "position": 7, "typeName": "ARRAY"},
			{"name": "skillOrders", "position": 8, "typeName": "ARRAY"},
		}}},
		"result": map[string]any{"dataArray": [][]any{row}},
	}
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"executeDatabricksQuery": map[string]any{"payload": payload}}})
	}))
	defer srv.Close()
	res, err := NewService("", srv.URL).Fetch(context.Background(), Request{Source: "blitz", ChampionID: 103, Position: "middle"})
	if err != nil {
		t.Fatal(err)
	}
	vars := got["variables"].(map[string]any)
	if vars["queue"] != "RANKED_SOLO_5X5" || vars["role"] != "MIDDLE" || vars["championId"] != "103" {
		t.Errorf("variables: %v", vars)
	}
	b := res.Builds[0]
	if len(b.Runes.Perks) != 9 || b.Core[0] != 6655 || b.Boots[0] != 3020 || b.Spells[0] != 4 {
		t.Errorf("build: %+v", b)
	}
	if len(b.Situational) != 1 || b.Situational[0] != 3157 {
		t.Errorf("situational: %v", b.Situational)
	}
	if b.BootsStats.WinRate != 0.5 || b.StarterStats.Games != 800 {
		t.Errorf("blitz item stats: boots %+v starter %+v", b.BootsStats, b.StarterStats)
	}
	if strings.Join(b.SkillOrder, "") != "QWE" {
		t.Errorf("skill order %v", b.SkillOrder)
	}
}

// fakeLCU records requests and answers from a small in-memory client.
type fakeLCU struct {
	calls  []string
	bodies map[string]json.RawMessage
	pages  string
	sets   string
}

func (f *fakeLCU) Do(_ context.Context, method, path string, body []byte) (lcu.Response, error) {
	key := method + " " + path
	f.calls = append(f.calls, key)
	if f.bodies == nil {
		f.bodies = map[string]json.RawMessage{}
	}
	f.bodies[key] = body
	ok := func(s string) (lcu.Response, error) { return lcu.Response{Status: 200, Body: json.RawMessage(s)}, nil }
	switch key {
	case "GET /lol-game-data/assets/v1/champion-summary.json":
		return ok(`[{"id":103,"name":"Ahri","alias":"Ahri"}]`)
	case "GET /lol-perks/v1/styles":
		return ok(`[{"id":8100,"slots":[{"type":"kKeyStone","perks":[8112,8128]},{"type":"kMixedRegularSplashable","perks":[8126,8139]},{"type":"kMixedRegularSplashable","perks":[8140,8137]},{"type":"kMixedRegularSplashable","perks":[8135,8106]},{"type":"kStatMod","perks":[5008,5005]},{"type":"kStatMod","perks":[5008,5010]},{"type":"kStatMod","perks":[5011,5001]}]},
		{"id":8200,"slots":[{"type":"kKeyStone","perks":[8229]},{"type":"kMixedRegularSplashable","perks":[8226]},{"type":"kMixedRegularSplashable","perks":[8210]},{"type":"kMixedRegularSplashable","perks":[8237]}]}]`)
	case "GET /lol-perks/v1/pages":
		return ok(f.pages)
	case "POST /lol-perks/v1/pages":
		return lcu.Response{Status: 400, Body: json.RawMessage(`{"message":"Max pages reached"}`)}, nil
	case "GET /lol-summoner/v1/current-summoner":
		return ok(`{"summonerId":42,"accountId":7}`)
	case "GET /lol-item-sets/v1/item-sets/42/sets":
		return ok(f.sets)
	case "GET /lol-champ-select/v1/session":
		return ok(`{"localPlayerCellId":2,"myTeam":[{"cellId":2,"spell1Id":4,"spell2Id":7}]}`)
	}
	return lcu.Response{Status: 204, Body: json.RawMessage("null")}, nil
}

func TestApply(t *testing.T) {
	f := &fakeLCU{
		pages: `[{"id":1,"name":"Défaut","isEditable":false,"current":true},{"id":50,"name":"Mid AP","isEditable":true,"isDeletable":true},{"id":51,"name":"OP.GG Lux Milieu","isEditable":true,"isDeletable":true}]`,
		sets:  `{"accountId":7,"itemSets":[{"uid":"mine","title":"Perso"},{"uid":"lolmobil-103-ranked","title":"ancien"}]}`,
	}
	res := &Result{ChampionID: 103, SourceName: "OP.GG", Mode: "ranked", Position: "middle"}
	// Perks without styles and out of order: styles are derived.
	b := Build{
		Runes:      Runes{Perks: []int{5008, 8226, 8112, 8126, 8140, 8135, 8210, 5008, 5001}},
		Spells:     []int{14, 4},
		Starter:    []int{1056, 2003, 2003},
		Core:       []int{6655, 4645},
		CoreStats:  Stats{WinRate: 0.55},
		Boots:      []int{3020},
		SkillOrder: []string{"Q", "E", "W"},
	}
	out := Apply(context.Background(), f, res, b, ApplyOptions{Runes: true, Items: true, Spells: true})
	if len(out.Errors) != 0 || len(out.Done) != 3 {
		t.Fatalf("apply: %+v (calls %v)", out, f.calls)
	}
	var page map[string]any
	_ = json.Unmarshal(f.bodies["PUT /lol-perks/v1/pages/51"], &page)
	if page["primaryStyleId"].(float64) != 8100 || page["subStyleId"].(float64) != 8200 || page["name"] != "OP.GG Ahri Milieu" {
		t.Errorf("rune page: %v", page)
	}
	perks := page["selectedPerkIds"].([]any)
	if perks[0].(float64) != 8112 || perks[4].(float64) != 8226 || perks[6].(float64) != 5008 {
		t.Errorf("perk order: %v", perks)
	}
	var spells map[string]float64
	_ = json.Unmarshal(f.bodies["PATCH /lol-champ-select/v1/session/my-selection"], &spells)
	if spells["spell1Id"] != 4 || spells["spell2Id"] != 14 {
		t.Errorf("Flash should stay on D: %v", spells)
	}
	var sets struct {
		AccountID int              `json:"accountId"`
		ItemSets  []map[string]any `json:"itemSets"`
	}
	_ = json.Unmarshal(f.bodies["PUT /lol-item-sets/v1/item-sets/42/sets"], &sets)
	if sets.AccountID != 7 || len(sets.ItemSets) != 2 || sets.ItemSets[0]["uid"] != "mine" {
		t.Fatalf("item sets: %+v", sets)
	}
	blocks := sets.ItemSets[1]["blocks"].([]any)
	starter := blocks[0].(map[string]any)["items"].([]any)
	if len(starter) != 2 || starter[1].(map[string]any)["count"].(float64) != 2 {
		t.Errorf("potions should be grouped: %v", starter)
	}
	if title := blocks[1].(map[string]any)["type"]; title != "Objets principaux — 55,0 % de victoires" {
		t.Errorf("core block title: %v", title)
	}
	if title := blocks[0].(map[string]any)["type"]; title != "Objets de départ" {
		t.Errorf("no stats, no rate: %v", title)
	}
	if title := sets.ItemSets[1]["title"]; title != "OP.GG Ahri Milieu · Q > E > W" {
		t.Errorf("item set title: %v", title)
	}
}

func TestWithFinalItems(t *testing.T) {
	res := &Result{Builds: []Build{{Situational: []int{3157, 1082, 2003, 9999}}}}
	final := map[int]bool{3157: true, 1082: false, 2003: false}
	got := WithFinalItems(res, final).Builds[0].Situational
	if len(got) != 2 || got[0] != 3157 || got[1] != 9999 {
		t.Errorf("filtered: %v", got)
	}
	if len(res.Builds[0].Situational) != 4 {
		t.Error("the cached result must not change")
	}
}
