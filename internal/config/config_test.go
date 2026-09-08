package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestConfiguration(t *testing.T) {
	cfg, err := LoadFrom(func(string) (string, bool) {
		return "", false
	})
	if err != nil || cfg.DefaultCountry != "US" || cfg.PollInterval != time.Hour || cfg.MaximumActiveTracksPerUser != 25 || cfg.CommandRateLimit != 20 || cfg.CommandRateWindow != time.Minute {
		t.Fatalf("invalid defaults: %v", err)
	}

	for _, values := range []map[string]string{
		{
			"COMMAND_RATE_LIMIT": "0",
		},
		{
			"COMMAND_RATE_WINDOW": "0s",
		},
		{
			"POLL_INTERVAL": "0s",
		},
		{
			"STEAM_MAXIMUM_CONCURRENCY": "0",
		},
		{
			"DEFAULT_COUNTRY": "USA",
		},
		{
			"DELIVERY_RETRY_INITIAL_DELAY": "2h",
			"DELIVERY_RETRY_MAXIMUM_DELAY": "1h",
		},
		{
			"DELIVERY_RETRY_MAXIMUM_DELAY": "73h",
		},
	} {
		if _, err := LoadFrom(func(key string) (string, bool) {
			value, ok := values[key]

			return value, ok
		}); err == nil {
			t.Fatalf("invalid config accepted: %v", values)
		}
	}

	cfg, err = LoadFrom(func(key string) (string, bool) {
		return "private-token-value", key == "BOT_TOKEN"
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(fmt.Sprintf("%+v", cfg), "private-token-value") {
		t.Fatal("config formatting exposed the bot token")
	}
}
