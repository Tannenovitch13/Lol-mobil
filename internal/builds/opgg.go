package builds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// OPGG reads the champion statistics OP.GG's site and desktop app use:
// GET {base}/api/{region}/champions/{mode}/{championId}/{position}?tier=...
type OPGG struct {
	BaseURL string // default https://lol-api-champion.op.gg
	HTTP    *http.Client
}

func (o *OPGG) Name() string { return "OP.GG" }

// LCU position names -> OP.GG.
var opggPositions = map[string]string{
	"top": "top", "jungle": "jungle", "middle": "mid", "mid": "mid",
	"bottom": "adc", "adc": "adc", "utility": "support", "support": "support",
}

// OP.GG -> LCU position names.
var lcuPositions = map[string]string{
	"top": "top", "jungle": "jungle", "mid": "middle", "adc": "bottom", "support": "utility",
}

type opggStats struct {
	WinRate  float64 `json:"win_rate"`
	PickRate float64 `json:"pick_rate"`
	Play     int     `json:"play"`
	Win      int     `json:"win"`
}

type opggSet struct {
	IDs      []int   `json:"ids"`
	PickRate float64 `json:"pick_rate"`
	Play     int     `json:"play"`
	Win      int     `json:"win"`
}

type opggRune struct {
	Primary   int     `json:"primary_page_id"`
	Secondary int     `json:"secondary_page_id"`
	PrimaryID []int   `json:"primary_rune_ids"`
	SubIDs    []int   `json:"secondary_rune_ids"`
	ShardIDs  []int   `json:"stat_mod_ids"`
	PickRate  float64 `json:"pick_rate"`
	Play      int     `json:"play"`
	Win       int     `json:"win"`
}

type opggResponse struct {
	Meta struct {
		Version string `json:"version"`
	} `json:"meta"`
	Data struct {
		Summary struct {
			AverageStats opggStats `json:"average_stats"`
			Positions    []struct {
				Name  string    `json:"name"`
				Stats opggStats `json:"stats"`
			} `json:"positions"`
		} `json:"summary"`
		Runes          []opggRune `json:"runes"`
		SummonerSpells []opggSet  `json:"summoner_spells"`
		StarterItems   []opggSet  `json:"starter_items"`
		CoreItems      []opggSet  `json:"core_items"`
		Boots          []opggSet  `json:"boots"`
		LastItems      []opggSet  `json:"last_items"`
		Skills         []struct {
			Order []any `json:"order"`
		} `json:"skills"`
		SkillMasteries []struct {
			IDs []string `json:"ids"`
		} `json:"skill_masteries"`
	} `json:"data"`
}

// rate normalises a rate to 0-1 (the site mixes fractions and percents).
func rate(v float64) float64 {
	if v > 1 {
		return v / 100
	}
	return v
}

func setStats(s opggSet) Stats {
	st := Stats{PickRate: rate(s.PickRate), Games: s.Play}
	if s.Play > 0 {
		st.WinRate = float64(s.Win) / float64(s.Play)
	}
	return st
}

func (o *OPGG) get(ctx context.Context, region, mode string, champ int, position, tier string) (*opggResponse, error) {
	base := strings.TrimRight(o.BaseURL, "/")
	if base == "" {
		base = "https://lol-api-champion.op.gg"
	}
	u := fmt.Sprintf("%s/api/%s/champions/%s/%d/%s?tier=%s", base,
		url.PathEscape(region), url.PathEscape(mode), champ, url.PathEscape(position), url.QueryEscape(tier))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) LolMobil")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OP.GG injoignable : %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OP.GG a répondu HTTP %d", resp.StatusCode)
	}
	var out opggResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("réponse OP.GG illisible : %w", err)
	}
	return &out, nil
}

// Fetch implements Provider.
func (o *OPGG) Fetch(ctx context.Context, req Request) (*Result, error) {
	region := req.Region
	if region == "" {
		region = "global"
	}
	tier := req.Tier
	if tier == "" {
		tier = "emerald_plus"
	}
	var data *opggResponse
	var err error
	position := ""
	switch req.Mode {
	case "ranked":
		position = opggPositions[strings.ToLower(req.Position)]
		probe := position
		if probe == "" {
			probe = "mid"
		}
		data, err = o.get(ctx, region, "ranked", req.ChampionID, probe, tier)
		if err != nil {
			return nil, err
		}
		if position == "" {
			// No role given: use the champion's most played one.
			best, bestPlay := probe, -1
			for _, p := range data.Data.Summary.Positions {
				name := opggPositions[strings.ToLower(p.Name)]
				if name != "" && p.Stats.Play > bestPlay {
					best, bestPlay = name, p.Stats.Play
				}
			}
			if best != probe {
				if data, err = o.get(ctx, region, "ranked", req.ChampionID, best, tier); err != nil {
					return nil, err
				}
			}
			position = best
		}
	case "aram", "urf":
		data, err = o.get(ctx, region, req.Mode, req.ChampionID, "none", tier)
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrUnsupported
	}

	res := &Result{Patch: data.Meta.Version, Tier: tier, Position: lcuPositions[position]}
	for _, p := range data.Data.Summary.Positions {
		if name := lcuPositions[opggPositions[strings.ToLower(p.Name)]]; name != "" {
			res.Positions = append(res.Positions, name)
		}
	}
	avg := data.Data.Summary.AverageStats
	res.Stats = Stats{WinRate: rate(avg.WinRate), PickRate: rate(avg.PickRate), Games: avg.Play}
	if res.Stats.WinRate == 0 && avg.Play > 0 {
		res.Stats.WinRate = float64(avg.Win) / float64(avg.Play)
	}

	d := data.Data
	first := func(sets []opggSet) []int {
		if len(sets) == 0 {
			return nil
		}
		return sets[0].IDs
	}
	var skillStart, skillOrder []string
	if len(d.Skills) > 0 {
		for _, v := range d.Skills[0].Order {
			skillStart = append(skillStart, skillKey(v))
		}
		skillOrder = skillOrderFromLevels(skillStart)
		if len(skillStart) > 6 {
			skillStart = skillStart[:6]
		}
	}
	if len(d.SkillMasteries) > 0 && len(d.SkillMasteries[0].IDs) >= 3 {
		skillOrder = d.SkillMasteries[0].IDs
	}

	for i, r := range d.Runes {
		if i >= 3 {
			break
		}
		perks := append(append(append([]int{}, r.PrimaryID...), r.SubIDs...), r.ShardIDs...)
		b := Build{
			Runes:      Runes{Primary: r.Primary, Sub: r.Secondary, Perks: perks},
			RuneStats:  Stats{PickRate: rate(r.PickRate), Games: r.Play},
			Spells:     first(d.SummonerSpells),
			Starter:    first(d.StarterItems),
			Boots:      first(d.Boots),
			SkillOrder: skillOrder,
			SkillStart: skillStart,
		}
		if r.Play > 0 {
			b.RuneStats.WinRate = float64(r.Win) / float64(r.Play)
		}
		core := 0
		if i < len(d.CoreItems) {
			core = i
		}
		if len(d.CoreItems) > 0 {
			b.Core = d.CoreItems[core].IDs
			b.CoreStats = setStats(d.CoreItems[core])
		}
		var last []int
		for _, s := range d.LastItems {
			last = append(last, s.IDs...)
		}
		b.Situational = without(uniqueInts(last), b.Core, b.Boots, b.Starter)
		if len(b.Situational) > 8 {
			b.Situational = b.Situational[:8]
		}
		if i == 0 {
			b.Label = "Le plus joué"
		} else {
			b.Label = fmt.Sprintf("Variante %d", i)
		}
		res.Builds = append(res.Builds, b)
	}
	if len(res.Builds) == 0 {
		return nil, fmt.Errorf("pas de build OP.GG pour ce champion dans ce rôle")
	}
	return res, nil
}

func skillKey(v any) string {
	switch x := v.(type) {
	case string:
		return strings.ToUpper(x)
	case float64:
		return [...]string{"Q", "W", "E", "R"}[(int(x)+3)%4]
	}
	return "?"
}

// without returns ids not present in the other lists.
func without(ids []int, others ...[]int) []int {
	skip := map[int]bool{}
	for _, l := range others {
		for _, v := range l {
			skip[v] = true
		}
	}
	var out []int
	for _, v := range ids {
		if !skip[v] {
			out = append(out, v)
		}
	}
	return out
}
