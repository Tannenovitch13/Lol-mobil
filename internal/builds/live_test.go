package builds

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive queries the real sites: LOLMOBIL_LIVE=1 go test ./internal/builds -run Live -v
func TestLive(t *testing.T) {
	if os.Getenv("LOLMOBIL_LIVE") == "" {
		t.Skip("set LOLMOBIL_LIVE=1 to query OP.GG / Blitz")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s := NewService("", "")
	sources := []string{"opgg", "blitz"}
	if only := os.Getenv("LOLMOBIL_LIVE_SOURCE"); only != "" {
		sources = []string{only}
	}
	for _, src := range sources {
		for _, req := range []Request{
			{Source: src, ChampionID: 103, Position: "middle"},
			{Source: src, ChampionID: 222},
			{Source: src, ChampionID: 103, Mode: "aram"},
		} {
			res, err := s.Fetch(ctx, req)
			if err != nil {
				t.Errorf("%s %+v: %v", src, req, err)
				continue
			}
			b := res.Builds[0]
			t.Logf("%s champ %d %s/%s patch %s: %d builds, wr %.3f; runes %+v spells %v start %v core %v boots %v next %v skills %v",
				src, req.ChampionID, res.Mode, res.Position, res.Patch, len(res.Builds), res.Stats.WinRate,
				b.Runes, b.Spells, b.Starter, b.Core, b.Boots, b.Situational, b.SkillOrder)
			if !b.Runes.Valid() && src == "opgg" {
				t.Errorf("invalid runes %+v", b.Runes)
			}
		}
	}
}
