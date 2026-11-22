package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/biyan113/jmp/internal/store"
)

const (
	colorAuto      = "auto"
	colorAlways    = "always"
	colorNever     = "never"
	colorANSI256   = "ansi256"
	colorTrueColor = "truecolor"
)

// AppConfig stores local UI and shell behavior preferences.
type AppConfig struct {
	Color string `json:"color"`
}

func defaultConfig() *AppConfig {
	return &AppConfig{Color: colorAuto}
}

// defaultDBPath honours JMP_DB so the shell integration (which never passes
// --db) can be pointed at a custom database.
func defaultDBPath() string {
	if p := os.Getenv("JMP_DB"); p != "" {
		return p
	}
	return store.DefaultPath()
}

// stateDir is where config.json, sync.json and sync state live: next to a
// custom database, otherwise in the platform data directory. Every auxiliary
// file follows the same rule.
func stateDir() string {
	if dbPath != "" && dbPath != store.DefaultPath() {
		return filepath.Dir(dbPath)
	}
	return store.DataDir()
}

func configFile() string {
	return filepath.Join(stateDir(), "config.json")
}

func loadConfig() (*AppConfig, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(configFile())
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", configFile(), err)
	}
	if !validColorMode(cfg.Color) {
		cfg.Color = colorAuto
	}
	return cfg, nil
}

func saveConfig(cfg *AppConfig) error {
	if !validColorMode(cfg.Color) {
		return fmt.Errorf("invalid color mode: %s", cfg.Color)
	}
	return writeJSONFile(configFile(), cfg)
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func validColorMode(mode string) bool {
	switch mode {
	case colorAuto, colorAlways, colorNever, colorANSI256, colorTrueColor:
		return true
	default:
		return false
	}
}
