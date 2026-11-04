package server

import "testing"

func TestAllowed(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/lol-gameflow/v1/session", true},
		{"POST", "/lol-matchmaking/v1/ready-check/accept", true},
		{"PATCH", "/lol-champ-select/v1/session/actions/3", true},
		{"PUT", "/lol-perks/v1/currentpage", true},
		{"GET", "/lol-game-data/assets/v1/champion-summary.json", true},
		{"POST", "/lol-game-data/assets/v1/champion-summary.json", false},
		{"GET", "/lol-summoner/v1/current-summoner", true},
		{"PUT", "/lol-summoner/v1/current-summoner/icon", false},
		{"POST", "/process-control/v1/process/quit", false},
		{"DELETE", "/lol-login/v1/session", false},
		{"GET", "/riotclient/auth-token", false},
		{"GET", "/lol-lobby/../riotclient/kill", false},
		{"GET", "lol-lobby/v2/lobby", false},
	}
	for _, c := range cases {
		if got := allowed(c.method, c.path); got != c.want {
			t.Errorf("allowed(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestMatches(t *testing.T) {
	if !matches("/lol-lobby/v2/received-invitations*", "/lol-lobby/v2/received-invitations/abc") {
		t.Error("prefix pattern should match")
	}
	if matches("/lol-lobby/v2/lobby", "/lol-lobby/v2/lobby/members") {
		t.Error("exact pattern should not match sub-paths")
	}
	if !observable("/lol-champ-select/v1/session") || observable("/riotclient/auth-token*") {
		t.Error("observable check wrong")
	}
}
