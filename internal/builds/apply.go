package builds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tannenovitch13/lol-mobil/internal/lcu"
)

// LCU is what Apply needs from the League client.
type LCU interface {
	Do(ctx context.Context, method, path string, body []byte) (lcu.Response, error)
}

// ApplyOptions chooses what to change.
type ApplyOptions struct {
	Runes, Items, Spells bool
	// InstallDir enables the item set file fallback (Config/Champions).
	InstallDir string
}

// ApplyResult tells what was changed (French messages for the phone).
type ApplyResult struct {
	Done   []string `json:"done"`
	Errors []string `json:"errors"`
}

// Apply writes a build into the client.
func Apply(ctx context.Context, cl LCU, res *Result, b Build, opt ApplyOptions) ApplyResult {
	var out ApplyResult
	champ := championInfo(ctx, cl, res.ChampionID)
	if opt.Runes {
		if err := applyRunes(ctx, cl, res, b, champ); err != nil {
			out.Errors = append(out.Errors, "Runes : "+err.Error())
		} else {
			out.Done = append(out.Done, "runes")
		}
	}
	if opt.Items {
		if err := applyItems(ctx, cl, res, b, champ, opt.InstallDir); err != nil {
			out.Errors = append(out.Errors, "Objets : "+err.Error())
		} else {
			out.Done = append(out.Done, "set d'objets")
		}
	}
	if opt.Spells && len(b.Spells) == 2 {
		if err := applySpells(ctx, cl, b.Spells); err != nil {
			out.Errors = append(out.Errors, "Sorts : "+err.Error())
		} else {
			out.Done = append(out.Done, "sorts")
		}
	}
	return out
}

func call(ctx context.Context, cl LCU, method, path string, body any, out any) error {
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return err
		}
	}
	resp, err := cl.Do(ctx, method, path, data)
	if err != nil {
		return err
	}
	if resp.Status < 200 || resp.Status > 299 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(resp.Body, &e)
		if e.Message != "" {
			return fmt.Errorf("%s (HTTP %d)", e.Message, resp.Status)
		}
		return fmt.Errorf("HTTP %d", resp.Status)
	}
	if out != nil {
		return json.Unmarshal(resp.Body, out)
	}
	return nil
}

type champion struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Alias string `json:"alias"`
}

func championInfo(ctx context.Context, cl LCU, id int) champion {
	var list []champion
	if call(ctx, cl, http.MethodGet, "/lol-game-data/assets/v1/champion-summary.json", nil, &list) == nil {
		for _, c := range list {
			if c.ID == id {
				return c
			}
		}
	}
	return champion{ID: id, Name: fmt.Sprintf("Champion %d", id)}
}

var positionNames = map[string]string{
	"top": "Haut", "jungle": "Jungle", "middle": "Milieu", "bottom": "Bas", "utility": "Support",
}

func label(res *Result, champ champion) string {
	parts := []string{res.SourceName, champ.Name}
	switch res.Mode {
	case "aram":
		parts = append(parts, "ARAM")
	case "urf":
		parts = append(parts, "URF")
	default:
		if p := positionNames[res.Position]; p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------- runes

type perkStyle struct {
	ID    int `json:"id"`
	Slots []struct {
		Type  string `json:"type"`
		Perks []int  `json:"perks"`
	} `json:"slots"`
}

// completeRunes fills styles derived from the perks (some sources only
// give perk ids) and puts the perks in client order.
func completeRunes(ctx context.Context, cl LCU, r Runes) (Runes, error) {
	if r.Valid() {
		return r, nil
	}
	var styles []perkStyle
	if err := call(ctx, cl, http.MethodGet, "/lol-perks/v1/styles", nil, &styles); err != nil {
		return r, err
	}
	styleOf := map[int]int{}
	keystone := map[int]bool{}
	shard := map[int]bool{}
	for _, s := range styles {
		for i, slot := range s.Slots {
			for _, p := range slot.Perks {
				if slot.Type == "kStatMod" {
					shard[p] = true
					continue
				}
				styleOf[p] = s.ID
				if i == 0 || slot.Type == "kKeyStone" {
					keystone[p] = true
				}
			}
		}
	}
	var key, primary, sub, shards []int
	for _, p := range r.Perks {
		if keystone[p] {
			key = append(key, p)
		}
	}
	if len(key) != 1 {
		return r, errors.New("page de runes incomplète")
	}
	if r.Primary == 0 {
		r.Primary = styleOf[key[0]]
	}
	for _, p := range r.Perks {
		switch {
		case keystone[p]:
		case shard[p]:
			shards = append(shards, p)
		case styleOf[p] == r.Primary:
			primary = append(primary, p)
		default:
			sub = append(sub, p)
			if r.Sub == 0 {
				r.Sub = styleOf[p]
			}
		}
	}
	r.Perks = append(append(append(key, primary...), sub...), shards...)
	if !r.Valid() {
		return r, errors.New("page de runes incomplète")
	}
	return r, nil
}

type perkPage struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	IsEditable  bool   `json:"isEditable"`
	IsDeletable bool   `json:"isDeletable"`
	Current     bool   `json:"current"`
}

// Pages LolMobil wrote are reused instead of filling the page slots.
func ourPage(name string) bool {
	return strings.HasPrefix(name, "OP.GG ") || strings.HasPrefix(name, "Blitz ")
}

func applyRunes(ctx context.Context, cl LCU, res *Result, b Build, champ champion) error {
	runes, err := completeRunes(ctx, cl, b.Runes)
	if err != nil {
		return err
	}
	name := label(res, champ)
	if len([]rune(name)) > 25 {
		name = string([]rune(name)[:25])
	}
	page := map[string]any{
		"name": name, "primaryStyleId": runes.Primary, "subStyleId": runes.Sub,
		"selectedPerkIds": runes.Perks, "current": true,
	}
	var pages []perkPage
	if err := call(ctx, cl, http.MethodGet, "/lol-perks/v1/pages", nil, &pages); err != nil {
		return err
	}
	// 1. Overwrite a page LolMobil made before.
	for _, p := range pages {
		if p.IsEditable && ourPage(p.Name) {
			return putPage(ctx, cl, p.ID, page)
		}
	}
	// 2. A new page.
	if call(ctx, cl, http.MethodPost, "/lol-perks/v1/pages", page, nil) == nil {
		return nil
	}
	// 3. No free slot: overwrite the current page if editable, else the
	// first editable one.
	for _, p := range pages {
		if p.Current && p.IsEditable {
			return putPage(ctx, cl, p.ID, page)
		}
	}
	for _, p := range pages {
		if p.IsEditable && p.IsDeletable {
			return putPage(ctx, cl, p.ID, page)
		}
	}
	return errors.New("aucune page de runes modifiable")
}

func putPage(ctx context.Context, cl LCU, id int, page map[string]any) error {
	page["id"] = id
	if err := call(ctx, cl, http.MethodPut, "/lol-perks/v1/pages/"+strconv.Itoa(id), page, nil); err != nil {
		return err
	}
	return call(ctx, cl, http.MethodPut, "/lol-perks/v1/currentpage", id, nil)
}

// ---------------------------------------------------------------- spells

const flash = 4

func applySpells(ctx context.Context, cl LCU, spells []int) error {
	var session struct {
		Local  int `json:"localPlayerCellId"`
		MyTeam []struct {
			CellID int `json:"cellId"`
			Spell1 int `json:"spell1Id"`
			Spell2 int `json:"spell2Id"`
		} `json:"myTeam"`
	}
	if err := call(ctx, cl, http.MethodGet, "/lol-champ-select/v1/session", nil, &session); err != nil {
		return errors.New("seulement pendant la sélection des champions")
	}
	s1, s2 := spells[0], spells[1]
	// Keep Flash on the key the player uses.
	for _, c := range session.MyTeam {
		if c.CellID != session.Local {
			continue
		}
		if (c.Spell1 == flash && s2 == flash) || (c.Spell2 == flash && s1 == flash) {
			s1, s2 = s2, s1
		}
	}
	return call(ctx, cl, http.MethodPatch, "/lol-champ-select/v1/session/my-selection",
		map[string]any{"spell1Id": s1, "spell2Id": s2}, nil)
}

// ---------------------------------------------------------------- items

type itemRef struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

type itemBlock struct {
	Type  string    `json:"type"`
	Items []itemRef `json:"items"`
}

func block(title string, ids []int) *itemBlock {
	if len(ids) == 0 {
		return nil
	}
	b := &itemBlock{Type: title}
	index := map[int]int{}
	for _, id := range ids {
		if i, ok := index[id]; ok {
			b.Items[i].Count++
			continue
		}
		index[id] = len(b.Items)
		b.Items = append(b.Items, itemRef{ID: strconv.Itoa(id), Count: 1})
	}
	return b
}

func blocks(b Build) []itemBlock {
	starter := "Objets de départ"
	if len(b.SkillOrder) >= 3 {
		starter += " · compétences " + strings.Join(b.SkillOrder, " > ")
	}
	var out []itemBlock
	for _, blk := range []*itemBlock{
		block(starter, b.Starter),
		block("Objets principaux", b.Core),
		block("Bottes", b.Boots),
		block("Selon la partie", b.Situational),
		block("Consommables", []int{2003, 2055}),
	} {
		if blk != nil {
			out = append(out, *blk)
		}
	}
	return out
}

func mapsFor(mode string) []int {
	switch mode {
	case "aram":
		return []int{12}
	}
	return []int{11}
}

func applyItems(ctx context.Context, cl LCU, res *Result, b Build, champ champion, installDir string) error {
	if len(b.Core) == 0 && len(b.Starter) == 0 {
		return errors.New("pas d'objets dans ce build")
	}
	title := label(res, champ)
	uid := fmt.Sprintf("lolmobil-%d-%s", res.ChampionID, res.Mode)
	set := map[string]any{
		"uid": uid, "title": title, "type": "custom", "map": "any", "mode": "any",
		"sortrank": 0, "startedFrom": "blank", "preferredItemSlots": []any{},
		"associatedChampions": []int{res.ChampionID}, "associatedMaps": mapsFor(res.Mode),
		"blocks": blocks(b),
	}
	err := putItemSet(ctx, cl, uid, set)
	if err == nil {
		return nil
	}
	// Fallback: the file the game reads in its install folder.
	if installDir != "" && champ.Alias != "" {
		if ferr := writeItemSetFile(installDir, champ.Alias, title, b); ferr == nil {
			return nil
		}
	}
	return err
}

func putItemSet(ctx context.Context, cl LCU, uid string, set map[string]any) error {
	var me struct {
		SummonerID int64 `json:"summonerId"`
		AccountID  int64 `json:"accountId"`
	}
	if err := call(ctx, cl, http.MethodGet, "/lol-summoner/v1/current-summoner", nil, &me); err != nil {
		return err
	}
	path := fmt.Sprintf("/lol-item-sets/v1/item-sets/%d/sets", me.SummonerID)
	var current struct {
		AccountID int64            `json:"accountId"`
		ItemSets  []map[string]any `json:"itemSets"`
	}
	if err := call(ctx, cl, http.MethodGet, path, nil, &current); err != nil {
		return err
	}
	sets := []map[string]any{}
	for _, s := range current.ItemSets {
		if id, _ := s["uid"].(string); id != uid {
			sets = append(sets, s)
		}
	}
	sets = append(sets, set)
	account := current.AccountID
	if account == 0 {
		account = me.AccountID
	}
	return call(ctx, cl, http.MethodPut, path, map[string]any{
		"accountId": account, "itemSets": sets, "timestamp": time.Now().UnixMilli(),
	}, nil)
}

// writeItemSetFile writes the legacy recommended item file the game also
// reads: <League>/Game/Config/Champions/<Alias>/Recommended/LolMobil.json.
func writeItemSetFile(installDir, alias, title string, b Build) error {
	dir := filepath.Join(installDir, "Game", "Config", "Champions", alias, "Recommended")
	if _, err := os.Stat(filepath.Join(installDir, "Game")); err != nil {
		dir = filepath.Join(installDir, "Config", "Champions", alias, "Recommended")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]any{
		"title": title, "type": "custom", "map": "any", "mode": "any",
		"priority": false, "sortrank": 0, "blocks": blocks(b),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "LolMobil.json"), data, 0o644)
}

// FinalItems returns the ids of items worth suggesting after the core build:
// completed items (nothing builds from them), not consumables. Unknown items
// are kept.
func FinalItems(ctx context.Context, cl LCU) (map[int]bool, error) {
	var items []struct {
		ID         int   `json:"id"`
		To         []int `json:"to"`
		PriceTotal int   `json:"priceTotal"`
	}
	if err := call(ctx, cl, http.MethodGet, "/lol-game-data/assets/v1/items.json", nil, &items); err != nil {
		return nil, err
	}
	final := map[int]bool{}
	for _, it := range items {
		final[it.ID] = len(it.To) == 0 && (it.PriceTotal == 0 || it.PriceTotal >= 1000)
	}
	return final, nil
}

// WithFinalItems returns a copy of res whose "next items" are only
// completed items.
func WithFinalItems(res *Result, final map[int]bool) *Result {
	if final == nil {
		return res
	}
	out := *res
	out.Builds = make([]Build, len(res.Builds))
	for i, b := range res.Builds {
		var keep []int
		for _, id := range b.Situational {
			if ok, known := final[id]; ok || !known {
				keep = append(keep, id)
			}
		}
		if len(keep) > 6 {
			keep = keep[:6]
		}
		b.Situational = keep
		out.Builds[i] = b
	}
	return &out
}
