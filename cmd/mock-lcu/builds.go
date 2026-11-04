package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Imitations of the OP.GG champion API and the Blitz GraphQL datalake (in
// their observed formats) so the builds feature can be tested offline, plus
// the client's item list and item sets.

var reOPGG = regexp.MustCompile(`^/api/([^/]+)/champions/([^/]+)/(\d+)/([^/]+)$`)

var mockItems = []map[string]any{
	{"id": 1056, "name": "Anneau de Doran"}, {"id": 2003, "name": "Potion de soin"},
	{"id": 2055, "name": "Balise de contrôle"}, {"id": 6655, "name": "Compagnon de Luden"},
	{"id": 4645, "name": "Flamme-ombre"}, {"id": 3089, "name": "Coiffe de Rabadon"},
	{"id": 3020, "name": "Chaussures du sorcier"}, {"id": 3157, "name": "Sablier de Zhonya"},
	{"id": 3135, "name": "Bâton du vide"}, {"id": 3165, "name": "Morellonomicon"},
}

func itemsJSON() []map[string]any {
	var out []map[string]any
	for _, it := range mockItems {
		c := map[string]any{}
		for k, v := range it {
			c[k] = v
		}
		c["iconPath"] = fmt.Sprintf("/lol-game-data/assets/ASSETS/Items/Icons2D/%d_mock.png", it["id"])
		out = append(out, c)
	}
	return out
}

func opggBuild(position string) map[string]any {
	set := func(pick float64, play, win int, ids ...int) map[string]any {
		return map[string]any{"ids": ids, "pick_rate": pick, "play": play, "win": win}
	}
	return map[string]any{
		"meta": map[string]any{"version": "16.22"},
		"data": map[string]any{
			"summary": map[string]any{
				"average_stats": map[string]any{"win_rate": 0.5123, "pick_rate": 0.084, "play": 120345},
				"positions": []any{
					map[string]any{"name": "MID", "stats": map[string]any{"play": 118000}},
					map[string]any{"name": "SUPPORT", "stats": map[string]any{"play": 2345}},
				},
			},
			"runes": []any{
				map[string]any{"primary_page_id": 8100, "secondary_page_id": 8200, "primary_rune_ids": []int{8112, 8126, 8140, 8135},
					"secondary_rune_ids": []int{8226, 8210}, "stat_mod_ids": []int{5008, 5008, 5001}, "pick_rate": 0.61, "play": 70000, "win": 36400},
				map[string]any{"primary_page_id": 8200, "secondary_page_id": 8300, "primary_rune_ids": []int{8229, 8226, 8210, 8237},
					"secondary_rune_ids": []int{8347, 8304}, "stat_mod_ids": []int{5008, 5008, 5001}, "pick_rate": 0.22, "play": 25000, "win": 12600},
			},
			"summoner_spells": []any{set(0.55, 64000, 33000, 14, 4)},
			"starter_items":   []any{set(0.8, 90000, 46000, 1056, 2003, 2003)},
			"core_items":      []any{set(0.3, 20000, 11000, 6655, 4645, 3089), set(0.12, 8000, 4300, 3165, 4645, 3089)},
			"boots":           []any{set(0.7, 80000, 41000, 3020)},
			"last_items":      []any{set(0.3, 1, 1, 3157), set(0.2, 1, 1, 3135)},
			"skills":          []any{map[string]any{"order": []string{"Q", "W", "E", "Q", "Q", "R", "Q", "W", "Q", "W", "R", "W", "W", "E", "E", "R", "E", "E"}}},
			"skill_masteries": []any{map[string]any{"ids": []string{"Q", "W", "E"}}},
		},
	}
}

func blitzPayload() map[string]any {
	cols := []string{"individual_position", "patch", "pick_rate", "runes", "summonerSpells", "startingItems", "coreItems", "situationalItems", "skillOrders"}
	var columns []map[string]any
	for i, c := range cols {
		columns = append(columns, map[string]any{"name": c, "position": i, "typeName": "STRING"})
	}
	row := []any{
		"MIDDLE", "16.22", 0.4,
		`[{"perks":[8229,8226,8210,8237,8347,8304,5008,5008,5001],"games":1000,"wins":530,"pick_rate":0.5}]`,
		`[{"summonerSpellIds":[4,14],"games":900,"wins":470,"pick_rate":0.6}]`,
		`[{"itemIds":[1056,2003,2003],"games":800,"wins":410,"pick_rate":0.7}]`,
		`[{"itemIds":[3165,4645,3089],"games":500,"wins":270,"pick_rate":0.3}]`,
		`[{"itemId":3020,"games":400,"wins":200},{"itemId":3157,"games":300,"wins":160}]`,
		`[{"skillOrder":[1,3,2,1,1,4,1,3,1,3,4,3,3,2,2,4,2,2]}]`,
	}
	return map[string]any{
		"manifest": map[string]any{"schema": map[string]any{"columns": columns}},
		"result":   map[string]any{"dataArray": []any{row}},
	}
}

// serveBuildSites answers the fake OP.GG and Blitz endpoints (no auth).
func (m *mock) serveBuildSites(w http.ResponseWriter, r *http.Request) bool {
	if mt := reOPGG.FindStringSubmatch(r.URL.Path); mt != nil {
		writeJSON(w, 200, opggBuild(mt[4]))
		return true
	}
	if r.URL.Path == "/graphql" && r.Method == http.MethodPost {
		writeJSON(w, 200, map[string]any{"data": map[string]any{"executeDatabricksQuery": map[string]any{"payload": blitzPayload()}}})
		return true
	}
	if r.URL.Path == "/mock/itemsets" {
		m.mu.Lock()
		defer m.mu.Unlock()
		writeJSON(w, 200, m.itemSets)
		return true
	}
	return false
}

// serveItemSets handles /lol-item-sets/v1/item-sets/<summonerId>/sets.
func (m *mock) serveItemSets(w http.ResponseWriter, r *http.Request, raw []byte) bool {
	if !strings.HasPrefix(r.URL.Path, "/lol-item-sets/v1/item-sets/1/sets") {
		return false
	}
	switch r.Method {
	case http.MethodGet:
		sets := m.itemSets
		if sets == nil {
			sets = map[string]any{"accountId": 7, "itemSets": []any{}, "timestamp": 0}
		}
		writeJSON(w, 200, sets)
	case http.MethodPut:
		var body map[string]any
		if json.Unmarshal(raw, &body) != nil {
			writeJSON(w, 400, map[string]any{"message": "bad body"})
			return true
		}
		m.itemSets = body
		w.WriteHeader(201)
	}
	return true
}
