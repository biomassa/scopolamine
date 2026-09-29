// Package config loads and saves scopolamine's settings file and resolves the
// directories the app uses.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const appName = "scopolamine"

// Config is persisted as JSON in ConfigDir()/config.json.
type Config struct {
	// DeveloperToken is an Apple Music developer JWT. Optional here: the
	// devtoken package also looks at the environment.
	DeveloperToken string `json:"developer_token,omitempty"`
	// UserToken is the Music-User-Token obtained through `scopolamine login`.
	UserToken string `json:"user_token,omitempty"`
	// Storefront overrides the account storefront (e.g. "us"). Empty means
	// ask Apple.
	Storefront string `json:"storefront,omitempty"`
	// AuthPort is the localhost port used by the browser login page.
	AuthPort int `json:"auth_port,omitempty"`
	// Volume is the last used volume, 0..1.
	Volume float64 `json:"volume,omitempty"`
	// Theme is the color theme (see the theme picker, T).
	Theme string `json:"theme,omitempty"`
	// LocalRoot is the folder of the local library. Empty means the music
	// folder of the user (XDG_MUSIC_DIR, else ~/Music).
	LocalRoot string `json:"local_root,omitempty"`

	path string
}

func defaults() *Config {
	return &Config{AuthPort: 7778, Volume: 1}
}

// ConfigDir is ~/.config/scopolamine (or $XDG_CONFIG_HOME/scopolamine).
func ConfigDir() string {
	if d, err := os.UserConfigDir(); err == nil && d != "" {
		return filepath.Join(d, appName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", appName)
}

// CacheDir is ~/.cache/scopolamine (or $XDG_CACHE_HOME/scopolamine).
func CacheDir() string {
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return filepath.Join(d, appName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", appName)
}

// MusicDir is the default local library folder: $XDG_MUSIC_DIR, else
// ~/Music.
func MusicDir() string {
	if d := os.Getenv("XDG_MUSIC_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Music")
}

// LocalLibrary is the local library folder of c.
func (c *Config) LocalLibrary() string {
	if c.LocalRoot != "" {
		return c.LocalRoot
	}
	return MusicDir()
}

// DefaultPath is the config file location.
func DefaultPath() string { return filepath.Join(ConfigDir(), "config.json") }

// Load reads path (DefaultPath when empty). A missing file yields defaults.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	cfg := defaults()
	cfg.path = path
	b, err := os.ReadFile(path) //nolint:gosec // user config path
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.AuthPort == 0 {
		cfg.AuthPort = defaults().AuthPort
	}
	return cfg, nil
}

// Save writes the config back to where it was loaded from, mode 0600 because
// it holds tokens.
func (c *Config) Save() error {
	path := c.path
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return os.Rename(tmp, path)
}
