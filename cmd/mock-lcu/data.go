package main

import "fmt"

type champ struct {
	ID    int
	Name  string
	Alias string
}

var champions = []champ{
	{103, "Ahri", "Ahri"}, {266, "Aatrox", "Aatrox"}, {84, "Akali", "Akali"}, {12, "Alistar", "Alistar"},
	{32, "Amumu", "Amumu"}, {22, "Ashe", "Ashe"}, {157, "Yasuo", "Yasuo"}, {238, "Zed", "Zed"},
	{99, "Lux", "Lux"}, {64, "Lee Sin", "LeeSin"}, {222, "Jinx", "Jinx"}, {412, "Thresh", "Thresh"},
	{89, "Leona", "Leona"}, {245, "Ekko", "Ekko"}, {517, "Sylas", "Sylas"}, {145, "Kai'Sa", "Kaisa"},
	{236, "Lucian", "Lucian"}, {117, "Lulu", "Lulu"}, {875, "Sett", "Sett"}, {360, "Samira", "Samira"},
	{86, "Garen", "Garen"}, {11, "Maître Yi", "MasterYi"}, {25, "Morgana", "Morgana"}, {51, "Caitlyn", "Caitlyn"},
}

func champByID(id int) champ {
	for _, c := range champions {
		if c.ID == id {
			return c
		}
	}
	return champ{id, fmt.Sprintf("Champion %d", id), fmt.Sprintf("Champ%d", id)}
}

func championSummary() []map[string]any {
	out := []map[string]any{{"id": -1, "name": "None", "alias": "None", "squarePortraitPath": ""}}
	for _, c := range champions {
		out = append(out, map[string]any{
			"id": c.ID, "name": c.Name, "alias": c.Alias,
			"squarePortraitPath": fmt.Sprintf("/lol-game-data/assets/v1/champion-icons/%d.png", c.ID),
			"roles":              []string{"mage"},
		})
	}
	return out
}

var skinPrefixes = []string{"Arcade", "Gardienne des étoiles", "PROJET :", "Lune de sang", "Pulsefire", "Hextech"}

// carouselSkins returns the skins of a champion as the client lists them.
func carouselSkins(champID int) []map[string]any {
	c := champByID(champID)
	tile := func(n int) string {
		return fmt.Sprintf("/lol-game-data/assets/ASSETS/Characters/%s/Skins/Skin%02d/Images/%s_splash_tile_%d.jpg", c.Alias, n, c.Alias, n)
	}
	skins := []map[string]any{{
		"id": champID * 1000, "championId": champID, "name": c.Name, "isBase": true,
		"unlocked": true, "ownership": map[string]any{"owned": true}, "disabled": false,
		"tilePath": tile(0), "childSkins": []any{},
	}}
	for i, prefix := range skinPrefixes {
		n := i + 1
		id := champID*1000 + n
		owned := n == 2
		skin := map[string]any{
			"id": id, "championId": champID, "name": prefix + " " + c.Name, "isBase": false,
			"unlocked": owned, "ownership": map[string]any{"owned": owned}, "disabled": false,
			"tilePath": tile(n), "childSkins": []any{},
		}
		if n == 3 {
			var chromas []any
			colors := [][]string{{"#e84057", "#7a1f2b"}, {"#3fbf7f", "#1d5e3c"}, {"#4a90e2", "#1d3c5e"}}
			for j, col := range colors {
				chromas = append(chromas, map[string]any{
					"id": champID*1000 + 50 + j, "name": fmt.Sprintf("%s %s (Chroma %d)", prefix, c.Name, j+1),
					"colors": col, "unlocked": false, "ownership": map[string]any{"owned": false},
					"chromaPreviewPath": fmt.Sprintf("/lol-game-data/assets/v1/champion-chroma-images/%d/%d.png", champID, champID*1000+50+j),
				})
			}
			skin["childSkins"] = chromas
		}
		skins = append(skins, skin)
	}
	return skins
}

func queues() []map[string]any {
	q := func(id int, desc, mode string, mapID int, cat string, ranked, positions bool) map[string]any {
		return map[string]any{
			"id": id, "name": desc, "description": desc, "shortName": desc, "gameMode": mode,
			"mapId": mapID, "category": cat, "queueAvailability": "Available", "isRanked": ranked,
			"showPositionSelector": positions, "isCustom": false,
		}
	}
	return []map[string]any{
		q(400, "Normale (Draft)", "CLASSIC", 11, "PvP", false, true),
		q(420, "Classée Solo/Duo", "CLASSIC", 11, "PvP", true, true),
		q(440, "Classée Flexible", "CLASSIC", 11, "PvP", true, true),
		q(490, "Partie rapide", "CLASSIC", 11, "PvP", false, false),
		q(450, "ARAM", "ARAM", 12, "PvP", false, false),
		q(2400, "ARAM : Chaos", "KIWI", 12, "PvP", false, false),
		q(1700, "Arène", "CHERRY", 30, "PvP", false, false),
		q(870, "Coop vs IA (Intro)", "CLASSIC", 11, "VersusAi", false, false),
		q(890, "Coop vs IA (Intermédiaire)", "CLASSIC", 11, "VersusAi", false, false),
		map[string]any{"id": 1300, "description": "Mode désactivé", "queueAvailability": "PlatformDisabled", "category": "PvP", "mapId": 11},
		// Custom game queues (16.x): not modes to queue for.
		map[string]any{"id": 3100, "description": "SR Blind Pick Custom", "gameMode": "CLASSIC", "mapId": 11, "category": "Custom", "isCustom": true, "queueAvailability": "Available"},
		map[string]any{"id": 3140, "description": "Multiplayer Practice Tool Custom", "gameMode": "PRACTICETOOL", "mapId": 11, "category": "Custom", "isCustom": true, "queueAvailability": "Available"},
		map[string]any{"id": 3270, "description": "ARAM: Mayhem", "gameMode": "KIWI", "mapId": 12, "category": "Custom", "isCustom": true, "queueAvailability": "Available"},
	}
}

func customQueue(id int) bool {
	for _, q := range queues() {
		if q["id"] == id && q["isCustom"] == true {
			return true
		}
	}
	return false
}

// customGames is /lol-game-queues/v1/custom: what the custom game screen offers.
func customGames() map[string]any {
	sub := func(mapID int, mode string, mutator, perTeam int) map[string]any {
		return map[string]any{
			"mapId": mapID, "gameMode": mode, "mutators": []any{map[string]any{"id": mutator}},
			"numPlayersPerTeam": perTeam, "maxPlayerCount": perTeam * 2, "minLevel": 0,
			"queueAvailability": "Available", "customSpectatorPolicies": []any{"AllAllowed", "NotAllowed"},
		}
	}
	return map[string]any{
		"queueAvailability": "Available", "spectatorSlotLimit": 4, "gameServerRegions": []any{},
		"spectatorPolicies": []any{"AllAllowed", "LobbyAllowed", "FriendsAllowed", "NotAllowed"},
		"subcategories":     []any{sub(11, "CLASSIC", 1, 5), sub(11, "PRACTICETOOL", 1, 5), sub(12, "ARAM", 4, 5)},
	}
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func spells() []map[string]any {
	s := func(id int, name string, modes ...string) map[string]any {
		return map[string]any{
			"id": id, "name": name, "description": name, "gameModes": modes,
			"iconPath": fmt.Sprintf("/lol-game-data/assets/DATA/Spells/Icons2D/spell_%d.png", id),
		}
	}
	all := []string{"CLASSIC", "ARAM", "URF"}
	return []map[string]any{
		s(4, "Saut éclair", all...), s(14, "Embrasement", all...), s(12, "Téléportation", "CLASSIC"),
		s(7, "Soins", all...), s(6, "Fantôme", all...), s(3, "Fatigue", all...), s(21, "Barrière", all...),
		s(11, "Châtiment", "CLASSIC"), s(1, "Purge", all...), s(32, "Boule de neige", "ARAM"),
	}
}

type style struct {
	ID        int
	Name      string
	Keystones []int
	Rows      [3][]int
}

var runeStyles = []style{
	{8000, "Précision", []int{8005, 8008, 8021, 8010}, [3][]int{{9101, 9111, 8009}, {9104, 9105, 9103}, {8014, 8017, 8299}}},
	{8100, "Domination", []int{8112, 8128, 9923}, [3][]int{{8126, 8139, 8143}, {8137, 8140, 8141}, {8135, 8105, 8106}}},
	{8200, "Sorcellerie", []int{8214, 8229, 8230}, [3][]int{{8224, 8226, 8275}, {8210, 8234, 8233}, {8237, 8232, 8236}}},
	{8400, "Volonté", []int{8437, 8439, 8465}, [3][]int{{8446, 8463, 8401}, {8429, 8444, 8473}, {8451, 8453, 8242}}},
	{8300, "Inspiration", []int{8351, 8360, 8369}, [3][]int{{8306, 8304, 8321}, {8313, 8352, 8345}, {8347, 8410, 8316}}},
}

var statShards = [3][]int{{5008, 5005, 5007}, {5008, 5010, 5001}, {5011, 5013, 5001}}

func stylesJSON() []map[string]any {
	var out []map[string]any
	for _, s := range runeStyles {
		var allowed []int
		for _, o := range runeStyles {
			if o.ID != s.ID {
				allowed = append(allowed, o.ID)
			}
		}
		slots := []map[string]any{{"type": "kKeyStone", "perks": s.Keystones}}
		for _, row := range s.Rows {
			slots = append(slots, map[string]any{"type": "kMixedRegularSplashable", "perks": row})
		}
		for _, row := range statShards {
			slots = append(slots, map[string]any{"type": "kStatMod", "perks": row})
		}
		out = append(out, map[string]any{
			"id": s.ID, "name": s.Name, "allowedSubStyles": allowed, "defaultSubStyle": allowed[0],
			"iconPath": fmt.Sprintf("/lol-game-data/assets/v1/perk-images/Styles/%d.png", s.ID),
			"slots":    slots,
		})
	}
	return out
}

func perksJSON() []map[string]any {
	seen := map[int]bool{}
	var out []map[string]any
	add := func(id int) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, map[string]any{
			"id": id, "name": fmt.Sprintf("Rune %d", id),
			"iconPath": fmt.Sprintf("/lol-game-data/assets/v1/perk-images/Styles/perk_%d.png", id),
		})
	}
	for _, s := range runeStyles {
		for _, k := range s.Keystones {
			add(k)
		}
		for _, row := range s.Rows {
			for _, p := range row {
				add(p)
			}
		}
	}
	for _, row := range statShards {
		for _, p := range row {
			add(p)
		}
	}
	return out
}
