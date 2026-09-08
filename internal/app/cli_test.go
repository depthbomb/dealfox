package app

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestCLIHelpAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"dealfox", "--help"},
		{"dealfox", "database", "--help"},
		{"dealfox", "deliveries", "retry", "--help"},
		{"dealfox", "commands", "--help"},
	} {
		var output bytes.Buffer
		if err := New(&output, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(output.String(), "USAGE:") {
			t.Fatalf("missing generated help: %s", output.String())
		}
	}

	for _, args := range [][]string{
		{"dealfox", "deliveries", "retry"},
		{"dealfox", "serve", "--unknown"},
		{"dealfox", "unknown"},
	} {
		if err := New(io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(t.Context(), args); err == nil {
			t.Fatalf("invalid CLI accepted: %v", args)
		}
	}
}

func TestZeroGuildCannotSelectGlobalScope(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	err := New(io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(t.Context(), []string{"dealfox", "commands", "--guild", "0", "--confirm"})
	if err == nil || !strings.Contains(err.Error(), "positive Discord snowflake") {
		t.Fatalf("zero guild was not rejected before publication: %v", err)
	}
}
