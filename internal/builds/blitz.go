package builds

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Blitz reads the build statistics the Blitz app uses, from its GraphQL
// "datalake" (a Databricks query returning rows). The row format is not
// documented: parsing is tolerant, and rune styles missing from the data are
// derived from the perks when the build is applied.
type Blitz struct {
	URL  string // default https://datalake.v2.iesdev.com/graphql
	HTTP *http.Client
}

func (b *Blitz) Name() string { return "Blitz" }

const blitzBuildsQuery = `query ChampionBuildsTagged($championId: String!, $matchupChampionId: String, $queue: String!, $role: String, $tier: String) {
  executeDatabricksQuery(game: LEAGUE, queryName: "prod_champion_builds_tags", params: [
    { name: "individual_position", value: $role }
    { name: "queue_id", value: $queue }
    { name: "champion_id", value: $championId }
    { name: "build_tier", value: $tier }
    { name: "match_up_champion_id", value: $matchupChampionId }
  ]) { payload }
}`

var blitzQueues = map[string]string{
	"ranked": "RANKED_SOLO_5X5",
	"aram":   "HOWLING_ABYSS_ARAM",
	"urf":    "URF",
}

// LCU position names -> Riot "individual position".
var blitzRoles = map[string]string{
	"top": "TOP", "jungle": "JUNGLE", "middle": "MIDDLE", "mid": "MIDDLE",
	"bottom": "BOTTOM", "adc": "BOTTOM", "utility": "UTILITY", "support": "UTILITY",
}

type databricksPayload struct {
	Manifest struct {
		Schema struct {
			Columns []struct {
				Name     string `json:"name"`
				Position int    `json:"position"`
				TypeName string `json:"typeName"`
			} `json:"columns"`
		} `json:"schema"`
	} `json:"manifest"`
	Result struct {
		DataArray [][]any `json:"dataArray"`
	} `json:"result"`
}

// Fetch implements Provider.
func (b *Blitz) Fetch(ctx context.Context, req Request) (*Result, error) {
	queue := blitzQueues[req.Mode]
	if queue == "" {
		return nil, ErrUnsupported
	}
	var role any
	if req.Mode == "ranked" {
		if r := blitzRoles[strings.ToLower(req.Position)]; r != "" {
			role = r
		}
	}
	var tier any
	if req.Tier != "" {
		tier = strings.ToUpper(req.Tier)
	}
	body, _ := json.Marshal(map[string]any{
		"query": blitzBuildsQuery,
		"variables": map[string]any{
			"championId": strconv.Itoa(req.ChampionID), "queue": queue, "role": role,
			"tier": tier, "matchupChampionId": nil,
		},
	})
	url := b.URL
	if url == "" {
		url = "https://datalake.v2.iesdev.com/graphql"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) LolMobil")
	resp, err := b.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Blitz injoignable : %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Blitz a répondu HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Execute struct {
				Payload json.RawMessage `json:"payload"`
			} `json:"executeDatabricksQuery"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("réponse Blitz illisible : %w", err)
	}
	if len(envelope.Errors) > 0 {
		return nil, fmt.Errorf("Blitz : %s", envelope.Errors[0].Message)
	}
	rows, err := decodeDatabricks(envelope.Data.Execute.Payload)
	if err != nil {
		return nil, err
	}
	return blitzResult(rows, req)
}

// decodeDatabricks turns the payload into rows keyed by column name, with
// nested JSON strings decoded.
func decodeDatabricks(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("pas de données Blitz pour ce champion")
	}
	// The payload may itself be a JSON string.
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		raw = json.RawMessage(asString)
	}
	var p databricksPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("format Blitz inattendu : %w", err)
	}
	var rows []map[string]any
	for _, values := range p.Result.DataArray {
		row := map[string]any{}
		for _, c := range p.Manifest.Schema.Columns {
			if c.Position < len(values) {
				row[c.Name] = deepJSON(values[c.Position])
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("pas de données Blitz pour ce champion")
	}
	return rows, nil
}

func deepJSON(v any) any {
	switch x := v.(type) {
	case string:
		t := strings.TrimSpace(x)
		if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			var out any
			if json.Unmarshal([]byte(t), &out) == nil {
				return deepJSON(out)
			}
		}
		return x
	case []any:
		for i := range x {
			x[i] = deepJSON(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = deepJSON(x[k])
		}
		return x
	}
	return v
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

func ints(v any) []int {
	var out []int
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if n := int(num(e)); n > 0 {
				out = append(out, n)
			}
		}
	case string:
		for _, part := range strings.FieldsFunc(x, func(r rune) bool { return r == ',' || r == ' ' || r == '[' || r == ']' }) {
			if n, err := strconv.Atoi(part); err == nil && n > 0 {
				out = append(out, n)
			}
		}
	}
	return out
}

func field(m map[string]any, names ...string) any {
	for _, n := range names {
		if v, ok := m[n]; ok && v != nil {
			return v
		}
	}
	return nil
}

func records(v any) []map[string]any {
	list, _ := v.([]any)
	var out []map[string]any
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

type blitzChoice struct {
	ids   []int
	stats Stats
}

func blitzChoices(v any, idNames ...string) []blitzChoice {
	var out []blitzChoice
	for _, r := range records(v) {
		ids := ints(field(r, idNames...))
		if len(ids) == 0 {
			continue
		}
		games := int(num(field(r, "games", "num_games")))
		wins := num(field(r, "wins", "num_win_games"))
		st := Stats{PickRate: rate(num(field(r, "pick_rate", "pickRate"))), Games: games}
		if games > 0 {
			st.WinRate = wins / float64(games)
		}
		out = append(out, blitzChoice{ids, st})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].stats.PickRate != out[j].stats.PickRate {
			return out[i].stats.PickRate > out[j].stats.PickRate
		}
		return out[i].stats.Games > out[j].stats.Games
	})
	return out
}

func blitzResult(rows []map[string]any, req Request) (*Result, error) {
	sort.SliceStable(rows, func(i, j int) bool {
		return num(field(rows[i], "pick_rate", "games")) > num(field(rows[j], "pick_rate", "games"))
	})
	res := &Result{Tier: req.Tier, Position: strings.ToLower(req.Position)}
	seenRole := map[string]bool{}
	for _, row := range rows {
		if pos, ok := row["individual_position"].(string); ok && pos != "" {
			p := strings.ToLower(pos)
			if !seenRole[p] {
				seenRole[p] = true
				res.Positions = append(res.Positions, p)
			}
			if res.Position == "" {
				res.Position = p
			}
		}
		if res.Patch == "" {
			if p, ok := row["patch"].(string); ok {
				res.Patch = p
			}
		}
	}
	for i, row := range rows {
		if len(res.Builds) >= 3 {
			break
		}
		b := Build{Label: fmt.Sprintf("Variante %d", i)}
		if i == 0 {
			b.Label = "Le plus joué"
		}
		if tag, ok := row["tag"].(string); ok && tag != "" && i > 0 {
			b.Label = tag
		}
		// Runes: a list of perk ids, possibly with styles.
		runeRecs := blitzChoices(field(row, "runes", "perks", "runePages"), "perks", "perkIds", "runeIds", "rune_ids", "ids", "runes")
		if len(runeRecs) > 0 {
			b.Runes.Perks = runeRecs[0].ids
			b.RuneStats = runeRecs[0].stats
			if r := records(field(row, "runes", "perks", "runePages")); len(r) > 0 {
				b.Runes.Primary = int(num(field(r[0], "primaryStyleId", "primary_style", "primaryStyle", "primary_page_id")))
				b.Runes.Sub = int(num(field(r[0], "subStyleId", "sub_style", "subStyle", "secondary_page_id")))
			}
		} else if ids := ints(field(row, "runes", "perks", "rune_ids")); len(ids) > 0 {
			b.Runes.Perks = ids
		}
		if s := blitzChoices(field(row, "summonerSpells", "summoner_spells"), "summonerSpellIds", "summoner_spell_ids", "ids"); len(s) > 0 {
			b.Spells = s[0].ids
		}
		if s := blitzChoices(field(row, "startingItems", "starting_items"), "itemIds", "item_ids", "ids"); len(s) > 0 {
			b.Starter = s[0].ids
		}
		if s := blitzChoices(field(row, "coreItems", "core_items"), "itemIds", "item_ids", "ids"); len(s) > 0 {
			b.Core = s[0].ids
			b.CoreStats = s[0].stats
		}
		var situational []int
		for _, s := range blitzChoices(field(row, "situationalItems", "situational_items"), "itemIds", "item_ids", "itemId", "ids") {
			situational = append(situational, s.ids...)
		}
		for _, r := range records(field(row, "situationalItems", "situational_items")) {
			if id := int(num(field(r, "itemId", "item_id"))); id > 0 {
				situational = append(situational, id)
			}
		}
		situational = uniqueInts(situational)
		for _, id := range situational {
			if bootIDs[id] && len(b.Boots) == 0 {
				b.Boots = []int{id}
			}
		}
		b.Situational = without(situational, b.Core, b.Boots, b.Starter)
		if len(b.Situational) > 8 {
			b.Situational = b.Situational[:8]
		}
		if s := records(field(row, "skillOrders", "skill_orders")); len(s) > 0 {
			for _, n := range ints(field(s[0], "skillOrder", "skill_order")) {
				b.SkillStart = append(b.SkillStart, [...]string{"Q", "W", "E", "R"}[(n+3)%4])
			}
			b.SkillOrder = skillOrderFromLevels(b.SkillStart)
			if len(b.SkillStart) > 6 {
				b.SkillStart = b.SkillStart[:6]
			}
		}
		if len(b.Runes.Perks) == 0 && len(b.Core) == 0 {
			continue
		}
		res.Builds = append(res.Builds, b)
	}
	if len(res.Builds) == 0 {
		return nil, fmt.Errorf("pas de build Blitz exploitable pour ce champion")
	}
	return res, nil
}

// bootIDs are the tier 2 boots (to sort Blitz's situational items).
var bootIDs = map[int]bool{
	3006: true, 3009: true, 3020: true, 3047: true, 3111: true, 3117: true, 3158: true,
	3005: true, 3013: true, 3170: true, 3171: true, 3172: true, 3173: true, 3174: true, 3175: true, 3176: true,
}
