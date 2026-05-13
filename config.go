package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bytenote/jmp/internal/store"
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

func configFile() string {
	if dbPath != "" && dbPath != store.DefaultPath() {
		return filepath.Join(filepath.Dir(dbPath), "config.json")
	}
	return filepath.Join(store.DataDir(), "config.json")
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
		return nil, err
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
	if err := os.MkdirAll(filepath.Dir(configFile()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configFile(), data, 0o644)
}

func validColorMode(mode string) bool {
	switch mode {
	case colorAuto, colorAlways, colorNever, colorANSI256, colorTrueColor:
		return true
	default:
		return false
	}
}
