package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("DEFAULT_COUNTRY=GB\nBOT_TOKEN=test-token\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(directory, ".env.local"), []byte("DEFAULT_COUNTRY=DE\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(directory)
	if err != nil || cfg.DefaultCountry != "DE" || cfg.BotToken == nil || cfg.BotToken.Release() != "test-token" {
		t.Fatalf("configuration directory was not loaded: %v", err)
	}

	t.Setenv("DEFAULT_COUNTRY", "US")
	cfg, err = loadConfig(directory)
	if err != nil || cfg.DefaultCountry != "US" {
		t.Fatalf("process environment did not take precedence: %v", err)
	}
}
