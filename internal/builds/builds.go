// Package builds fetches popular champion builds from build sites (OP.GG,
// Blitz) and applies them to the League client: rune page, item set (shown
// in the in-game shop) and summoner spells.
//
// The sites have no public API: these are the endpoints their own apps and
// pages use, so they may change without notice. Each provider turns its
// data into the same Build type.
package builds

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tannenovitch13/lol-mobil/internal/netx"
)

// Request selects the builds to fetch.
type Request struct {
	Source     string `json:"source"`     // "opgg" or "blitz"
	ChampionID int    `json:"championId"` // League champion id
	// Position: top, jungle, middle, bottom, utility (LCU names), or ""
	// to let the site pick the champion's main role.
	Position string `json:"position"`
	// Mode: ranked (Summoner's Rift), aram, urf.
	Mode string `json:"mode"`
	// Tier filter, site specific ("" = provider default).
	Tier string `json:"tier"`
	// Region filter for OP.GG ("" = global).
	Region string `json:"region"`
}

// Runes is a rune page: styles and the 9 perk ids in client order
// (keystone, 3 primary, 2 secondary, 3 stat shards).
type Runes struct {
	Primary int   `json:"primaryStyleId"`
	Sub     int   `json:"subStyleId"`
	Perks   []int `json:"selectedPerkIds"`
}

// Valid reports whether the page can be sent to the client.
func (r Runes) Valid() bool { return r.Primary > 0 && r.Sub > 0 && len(r.Perks) == 9 }

// Stats of a build choice.
type Stats struct {
	WinRate  float64 `json:"winRate"`  // 0-1
	PickRate float64 `json:"pickRate"` // 0-1
	Games    int     `json:"games"`
}

// Build is one popular way to play the champion.
type Build struct {
	Label        string   `json:"label"`
	Runes        Runes    `json:"runes"`
	RuneStats    Stats    `json:"runeStats"`
	Spells       []int    `json:"spells"`
	Starter      []int    `json:"starter"`
	StarterStats Stats    `json:"starterStats"`
	Core         []int    `json:"core"`
	CoreStats    Stats    `json:"coreStats"`
	Boots        []int    `json:"boots"`
	BootsStats   Stats    `json:"bootsStats"`
	Situational  []int    `json:"situational"`
	SkillOrder   []string `json:"skillOrder"` // e.g. ["Q","E","W"] (max order)
	SkillStart   []string `json:"skillStart"` // first levels, e.g. ["Q","E","W","Q"]
}

// Result of a fetch.
type Result struct {
	Source     string   `json:"source"`
	SourceName string   `json:"sourceName"`
	ChampionID int      `json:"championId"`
	Position   string   `json:"position"`  // LCU name of the role used
	Positions  []string `json:"positions"` // roles the site has data for
	Mode       string   `json:"mode"`
	Patch      string   `json:"patch"`
	Tier       string   `json:"tier"`
	Stats      Stats    `json:"stats"` // champion overall in that role
	Builds     []Build  `json:"builds"`
	FetchedAt  int64    `json:"fetchedAt"`
	// Set when the chosen site could not be reached and these builds come
	// from OP.GG instead: its name and why.
	FallbackFrom   string `json:"fallbackFrom,omitempty"`
	FallbackReason string `json:"fallbackReason,omitempty"`
}

// Provider fetches builds from one site.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, req Request) (*Result, error)
}

// ErrUnsupported is returned for modes a site has no data for.
var ErrUnsupported = errors.New("mode non pris en charge par cette source")

// Service picks the provider and caches results for a while.
type Service struct {
	Providers map[string]Provider

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at  time.Time
	res *Result
}

const cacheFor = 30 * time.Minute

// NewService returns the default providers. Base URLs can be overridden
// (tests, or if a site moves).
func NewService(opggURL, blitzURL string) *Service {
	client := &http.Client{Timeout: 20 * time.Second, Transport: netx.Transport()}
	return &Service{Providers: map[string]Provider{
		"opgg":  &OPGG{BaseURL: opggURL, HTTP: client},
		"blitz": &Blitz{URL: blitzURL, HTTP: client},
	}}
}

// Fetch returns the builds for a request (cached for 30 minutes).
func (s *Service) Fetch(ctx context.Context, req Request) (*Result, error) {
	if req.Source == "" {
		req.Source = "opgg"
	}
	if req.Mode == "" {
		req.Mode = "ranked"
	}
	p := s.Providers[req.Source]
	if p == nil {
		return nil, fmt.Errorf("source inconnue %q", req.Source)
	}
	if req.ChampionID <= 0 {
		return nil, errors.New("aucun champion choisi")
	}
	key := cacheKey(req)
	if res := s.cached(key, cacheFor); res != nil {
		return res, nil
	}
	res, err := p.Fetch(ctx, req)
	if err != nil {
		err = friendlyError(p.Name(), err)
		alt := s.Providers["opgg"]
		if req.Source == "opgg" || alt == nil || errors.Is(err, ErrUnsupported) {
			return nil, err
		}
		// The site cannot be reached from this PC (network, DNS, site
		// changed): OP.GG's builds rather than nothing.
		altReq := req
		altReq.Source = "opgg"
		fallback, altErr := s.Fetch(ctx, altReq)
		if altErr != nil {
			return nil, err
		}
		copied := *fallback
		copied.FallbackFrom = p.Name()
		copied.FallbackReason = err.Error()
		// Try the site again in a few minutes, not at every tap.
		s.store(key, &copied, time.Now().Add(-(cacheFor - 5*time.Minute)))
		return &copied, nil
	}
	res.Source = req.Source
	res.SourceName = p.Name()
	res.ChampionID = req.ChampionID
	res.Mode = req.Mode
	res.FetchedAt = time.Now().UnixMilli()
	s.store(key, res, time.Now())
	return res, nil
}

func cacheKey(req Request) string {
	return fmt.Sprintf("%s|%d|%s|%s|%s|%s", req.Source, req.ChampionID, req.Position, req.Mode, req.Tier, req.Region)
}

func (s *Service) cached(key string, maxAge time.Duration) *Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cache[key]; ok && time.Since(c.at) < maxAge {
		return c.res
	}
	return nil
}

func (s *Service) store(key string, res *Result, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]cached{}
	}
	s.cache[key] = cached{at, res}
}

// friendlyError explains network failures in plain words.
func friendlyError(site string, err error) error {
	if errors.Is(err, ErrUnsupported) {
		return err
	}
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &dnsErr):
		return fmt.Errorf("%s injoignable depuis ce PC : adresse %s introuvable (DNS du réseau)", site, dnsErr.Name)
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return fmt.Errorf("%s ne répond pas (délai dépassé)", site)
	}
	return err
}

// ModeForQueue maps a gameflow game mode to a build mode.
func ModeForQueue(gameMode string) string {
	switch strings.ToUpper(gameMode) {
	case "ARAM", "KIWI": // KIWI: ARAM Mayhem
		return "aram"
	case "URF", "ARURF":
		return "urf"
	case "CHERRY":
		return "arena"
	}
	return "ranked"
}

// uniqueInts keeps the first occurrence of each positive id.
func uniqueInts(lists ...[]int) []int {
	seen := map[int]bool{}
	var out []int
	for _, l := range lists {
		for _, v := range l {
			if v > 0 && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// skillOrderFromLevels returns the max order (e.g. Q>E>W) from the first
// skill levels, ignoring R.
func skillOrderFromLevels(levels []string) []string {
	count := map[string]int{}
	firstMax := map[string]int{}
	for i, k := range levels {
		if k == "R" {
			continue
		}
		count[k]++
		if count[k] == 5 {
			firstMax[k] = i
		}
	}
	keys := []string{"Q", "W", "E"}
	sort.SliceStable(keys, func(a, b int) bool {
		fa, oka := firstMax[keys[a]]
		fb, okb := firstMax[keys[b]]
		switch {
		case oka && okb:
			return fa < fb
		case oka != okb:
			return oka
		}
		return count[keys[a]] > count[keys[b]]
	})
	return keys
}
