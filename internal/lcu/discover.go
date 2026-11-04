// Package lcu finds the running League client and talks to its local API
// (the "LCU" API): REST requests and the WAMP event websocket.
package lcu

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Credentials are what the League client writes in its lockfile.
type Credentials struct {
	Port     int
	Password string
	Protocol string // "https" for the real client
	// BaseURL overrides Port/Protocol (used by the mock client in tests).
	BaseURL string
	// Source says where the credentials were found (for logs).
	Source string
	// InstallDir is the League install folder, when known.
	InstallDir string
}

// URL returns the base URL of the LCU API.
func (c Credentials) URL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	proto := c.Protocol
	if proto == "" {
		proto = "https"
	}
	return fmt.Sprintf("%s://127.0.0.1:%d", proto, c.Port)
}

// ErrNotRunning means no League client could be found.
var ErrNotRunning = errors.New("League client not found")

// ParseLockfile parses "LeagueClient:pid:port:password:protocol".
func ParseLockfile(content string) (Credentials, error) {
	parts := strings.Split(strings.TrimSpace(content), ":")
	if len(parts) < 5 {
		return Credentials{}, fmt.Errorf("unexpected lockfile content")
	}
	port, err := strconv.Atoi(parts[2])
	if err != nil || port <= 0 {
		return Credentials{}, fmt.Errorf("invalid port in lockfile")
	}
	return Credentials{Port: port, Password: parts[3], Protocol: parts[4]}, nil
}

// ReadLockfile reads the lockfile in a League install folder.
func ReadLockfile(installDir string) (Credentials, error) {
	path := filepath.Join(installDir, "lockfile")
	data, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	creds, err := ParseLockfile(string(data))
	if err != nil {
		return Credentials{}, err
	}
	creds.Source = path
	return creds, nil
}

var (
	reAppPort   = regexp.MustCompile(`--app-port=(\d+)`)
	reAuthToken = regexp.MustCompile(`--remoting-auth-token=([\w-]+)`)
	reInstall   = regexp.MustCompile(`--install-directory=([^"]+?)"?\s+"?--`)
)

// ParseCommandLine extracts the API credentials from a LeagueClientUx.exe
// command line.
func ParseCommandLine(cmd string) (Credentials, string, bool) {
	p := reAppPort.FindStringSubmatch(cmd)
	t := reAuthToken.FindStringSubmatch(cmd)
	if p == nil || t == nil {
		return Credentials{}, "", false
	}
	port, err := strconv.Atoi(p[1])
	if err != nil {
		return Credentials{}, "", false
	}
	install := ""
	if m := reInstall.FindStringSubmatch(cmd); m != nil {
		install = strings.TrimSpace(m[1])
	}
	return Credentials{Port: port, Password: t[1], Protocol: "https", Source: "LeagueClientUx command line"}, install, true
}

// candidateInstallDirs lists the folders that usually hold League.
func candidateInstallDirs(configured string) []string {
	var dirs []string
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" {
			return
		}
		d = filepath.Clean(filepath.FromSlash(d))
		for _, existing := range dirs {
			if strings.EqualFold(existing, d) {
				return
			}
		}
		dirs = append(dirs, d)
	}
	add(configured)
	for _, d := range riotMetadataInstallDirs() {
		add(d)
	}
	for _, d := range platformDefaultDirs() {
		add(d)
	}
	return dirs
}

var reProductPath = regexp.MustCompile(`product_install_full_path:\s*"?([^"\r\n]+)"?`)

// riotMetadataInstallDirs reads the Riot Client metadata files that record
// where League is installed (Windows only; harmless elsewhere).
func riotMetadataInstallDirs() []string {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		return nil
	}
	var dirs []string
	settings := filepath.Join(programData, "Riot Games", "Metadata", "league_of_legends.live", "league_of_legends.live.product_settings.yaml")
	if data, err := os.ReadFile(settings); err == nil {
		if m := reProductPath.FindStringSubmatch(string(data)); m != nil {
			dirs = append(dirs, m[1])
		}
	}
	installs := filepath.Join(programData, "Riot Games", "RiotClientInstalls.json")
	if data, err := os.ReadFile(installs); err == nil {
		var parsed struct {
			AssociatedClient map[string]string `json:"associated_client"`
		}
		if json.Unmarshal(data, &parsed) == nil {
			for dir := range parsed.AssociatedClient {
				if strings.Contains(strings.ToLower(dir), "league") {
					dirs = append(dirs, dir)
				}
			}
		}
	}
	return dirs
}

// Discover finds the credentials of the running client. It tries the
// lockfile in known folders first (cheap), then asks the OS for the
// LeagueClientUx process command line when allowProcessScan is set.
func Discover(configuredDir string, allowProcessScan bool) (Credentials, string, error) {
	for _, dir := range candidateInstallDirs(configuredDir) {
		if creds, err := ReadLockfile(dir); err == nil {
			creds.InstallDir = dir
			return creds, dir, nil
		}
	}
	if allowProcessScan {
		if creds, install, ok := scanProcess(); ok {
			creds.InstallDir = install
			return creds, install, nil
		}
	}
	return Credentials{}, "", ErrNotRunning
}
