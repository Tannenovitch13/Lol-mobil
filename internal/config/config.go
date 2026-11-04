// Package config stores the companion settings (port, pairing secrets,
// League path, options) in a small JSON file next to the user's profile.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
)

// Config is persisted as JSON. Zero values are filled by Load.
type Config struct {
	// Port of the HTTP/WebSocket server the phone connects to.
	Port int `json:"port"`
	// Token authenticates the phone. It is handed out after a correct PIN
	// or read from the QR code.
	Token string `json:"token"`
	// PIN is the short code typed on the phone to pair it.
	PIN string `json:"pin"`
	// LeaguePath is an optional League of Legends install folder (the one
	// holding the lockfile). Detected automatically when empty.
	LeaguePath string `json:"leaguePath,omitempty"`
	// AutoAccept accepts every ready check without waiting for the phone.
	AutoAccept bool `json:"autoAccept"`
	// PenguPluginsDir is an optional Pengu Loader plugins folder where the
	// LolMobil-Remote plugin is installed. Detected (Rose) when empty.
	PenguPluginsDir string `json:"penguPluginsDir,omitempty"`

	path string
	mu   sync.Mutex
}

// DefaultPort is used when the config file does not set one.
const DefaultPort = 8787

// Dir returns the folder holding the config file.
func Dir() (string, error) {
	if d := os.Getenv("LOLMOBIL_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "LolMobil"), nil
}

// Load reads the config file, creating it with fresh secrets if needed.
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c := &Config{path: filepath.Join(dir, "config.json")}
	data, err := os.ReadFile(c.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("config %s is invalid: %w", c.path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}
	changed := false
	if c.Port == 0 {
		c.Port = DefaultPort
		changed = true
	}
	if len(c.Token) < 32 {
		c.Token = NewToken()
		changed = true
	}
	if len(c.PIN) != 6 {
		c.PIN = NewPIN()
		changed = true
	}
	if changed {
		if err := c.Save(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Path is the location of the config file.
func (c *Config) Path() string { return c.path }

// Save writes the config file.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Update changes the config under its lock and saves it.
func (c *Config) Update(fn func(c *Config)) error {
	c.mu.Lock()
	fn(c)
	c.mu.Unlock()
	return c.Save()
}

// GetAutoAccept reads the auto-accept option.
func (c *Config) GetAutoAccept() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.AutoAccept
}

// NewToken returns a random 32-byte hex token.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// NewPIN returns a random 6-digit code.
func NewPIN() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%06d", n.Int64())
}
