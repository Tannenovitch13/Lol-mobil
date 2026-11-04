package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/tannenovitch13/lol-mobil/internal/lcu"
)

// Static game data the phone loads in champion select. On a real client
// these answers are large (full rune, item and queue descriptions), which
// is slow to send, especially through remote access. The companion keeps a
// trimmed copy per client session, with only the fields the phone uses.
var staticFields = map[string][]string{
	"/lol-game-data/assets/v1/champion-summary.json": {"id", "name", "alias", "squarePortraitPath"},
	"/lol-game-data/assets/v1/items.json":            {"id", "name", "iconPath", "to", "priceTotal"},
	"/lol-game-data/assets/v1/summoner-spells.json":  {"id", "name", "iconPath", "gameModes"},
	"/lol-perks/v1/perks":                            {"id", "name", "iconPath"},
	"/lol-perks/v1/styles":                           {"id", "name", "iconPath", "slots", "allowedSubStyles", "defaultSubStyle"},
	"/lol-game-queues/v1/queues": {"id", "name", "description", "detailedDescription", "shortName", "gameMode",
		"mapId", "category", "queueAvailability", "isRanked", "showPositionSelector", "isCustom"},
}

type staticCache struct {
	mu     sync.Mutex
	client *lcu.Client
	data   map[string]json.RawMessage
	locks  map[string]*sync.Mutex
}

// trim keeps the listed fields of every object of a JSON array.
func trim(raw json.RawMessage, fields []string) (json.RawMessage, error) {
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := make([]map[string]json.RawMessage, 0, len(list))
	for _, obj := range list {
		kept := make(map[string]json.RawMessage, len(fields))
		for _, f := range fields {
			if v, ok := obj[f]; ok {
				kept[f] = v
			}
		}
		out = append(out, kept)
	}
	return json.Marshal(out)
}

// static returns the trimmed answer for a static data path (ok=false for
// other paths). Concurrent requests for the same path share one call.
func (s *Server) static(ctx context.Context, cl *lcu.Client, path string) (json.RawMessage, bool, error) {
	fields, ok := staticFields[path]
	if !ok {
		return nil, false, nil
	}
	c := &s.staticData
	c.mu.Lock()
	if c.client != cl {
		c.client = cl
		c.data = map[string]json.RawMessage{}
		c.locks = map[string]*sync.Mutex{}
	}
	if data, ok := c.data[path]; ok {
		c.mu.Unlock()
		return data, true, nil
	}
	lock := c.locks[path]
	if lock == nil {
		lock = &sync.Mutex{}
		c.locks[path] = lock
	}
	c.mu.Unlock()

	lock.Lock()
	defer lock.Unlock()
	c.mu.Lock()
	if data, ok := c.data[path]; ok && c.client == cl {
		c.mu.Unlock()
		return data, true, nil
	}
	c.mu.Unlock()

	resp, err := cl.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, true, err
	}
	if resp.Status != http.StatusOK {
		return nil, true, fmt.Errorf("HTTP %d", resp.Status)
	}
	data, err := trim(resp.Body, fields)
	if err != nil {
		// Unexpected shape: send it untouched rather than failing.
		data = resp.Body
	}
	c.mu.Lock()
	if c.client == cl {
		c.data[path] = data
	}
	c.mu.Unlock()
	return data, true, nil
}
