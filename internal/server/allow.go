package server

import (
	"net/http"
	"strings"
)

// The phone may only reach the parts of the LCU API it needs. Paths outside
// these prefixes (login, client shutdown, store, settings...) are refused.
var readWritePrefixes = []string{
	"/lol-gameflow/",
	"/lol-lobby/",
	"/lol-lobby-team-builder/",
	"/lol-matchmaking/",
	"/lol-champ-select/",
	"/lol-champ-select-legacy/",
	"/lol-perks/",
	"/lol-end-of-game/",
	"/lol-pre-end-of-game/",
	"/lol-honor-v2/",
	"/lol-suggested-players/",
}

var readOnlyPrefixes = []string{
	"/lol-summoner/v1/current-summoner",
	"/lol-summoner/v1/summoners",
	"/lol-summoner/v2/summoners",
	"/lol-game-data/",
	"/lol-game-queues/",
	"/lol-champions/",
	"/lol-collections/",
	"/lol-chat/v1/me",
	"/lol-chat/v1/friends",
	"/lol-maps/",
	"/lol-platform-config/v1/namespaces/LcuSocial",
	"/riotclient/region-locale",
}

func hasPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// allowed reports whether the phone may send this request to the LCU.
func allowed(method, path string) bool {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return false
	}
	if hasPrefix(path, readWritePrefixes) {
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			return true
		}
		return false
	}
	return method == http.MethodGet && hasPrefix(path, readOnlyPrefixes)
}

// observable reports whether the phone may follow events of this path.
func observable(path string) bool {
	path = strings.TrimSuffix(path, "*")
	return allowed(http.MethodGet, path)
}

// matches reports whether an event URI matches an observed pattern. A
// pattern ending with "*" matches every URI starting with it.
func matches(pattern, uri string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(uri, pattern[:len(pattern)-1])
	}
	return pattern == uri
}
