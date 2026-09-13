package app

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/depthbomb/argon"
)

func TestCLIValidation(t *testing.T) {
	for _, args := range [][]string{
		{"deliveries", "retry"},
		{"deliveries", "cancel"},
		{"freegames", "delivery"},
		{"serve", "--unknown"},
		{"unknown"},
		{"database", "unknown"},
		{"serve", "unexpected"},
		{"serve", "--", "unexpected"},
		{"commands", "--confirm", "unexpected"},
		{"deliveries", "list-dead", "--limit", "invalid"},
		{"deliveries", "list-dead", "--limit", "0"},
		{"deliveries", "list-dead", "--limit", "1001"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			result := New(io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil))).Evaluate(t.Context(), args)
			if result.Err == nil {
				t.Fatalf("invalid CLI accepted: %v", args)
			}

			last := args[len(args)-1]
			if last == "0" || last == "1001" {
				if result.Err.Error() != "limit must be between 1 and 1000" {
					t.Fatalf("limit was not validated before loading configuration: %v", result.Err)
				}
			} else if _, ok := errors.AsType[*argon.ParseError](result.Err); !ok {
				t.Fatalf("invalid input reached the command handler: %v", result.Err)
			}
		})
	}
}

func TestCLIOptions(t *testing.T) {
	for _, args := range [][]string{
		{"--config-dir", "config directory", "deliveries", "list-dead"},
		{"deliveries", "--config-dir", "config directory", "list-dead"},
		{"deliveries", "list-dead", "--config-dir=config directory"},
	} {
		inv, err := New(io.Discard, nil).Parse(args)
		if err != nil {
			t.Fatal(err)
		}

		if inv.String("config-dir") != "config directory" || inv.Int("limit") != 50 {
			t.Fatalf("global option or limit default changed: %v", args)
		}
	}

	for _, test := range []struct {
		args []string
		flag string
		want bool
	}{
		{
			args: []string{"commands"},
			flag: "confirm",
		},
		{
			args: []string{"commands", "--confirm"},
			flag: "confirm",
			want: true,
		},
		{
			args: []string{"commands", "--confirm=false"},
			flag: "confirm",
		},
		{
			args: []string{"freegames", "delivery", "--id", "test"},
			flag: "retry",
		},
		{
			args: []string{"freegames", "delivery", "--id=test", "--retry"},
			flag: "retry",
			want: true,
		},
	} {
		inv, err := New(io.Discard, nil).Parse(test.args)
		if err != nil {
			t.Fatal(err)
		}

		if inv.Bool(test.flag) != test.want {
			t.Fatalf("incorrect %s for %v", test.flag, test.args)
		}
	}

	inv, err := New(io.Discard, nil).Parse([]string{"freegames", "scan"})
	if err != nil || inv.String("sources") != "all" {
		t.Fatalf("incorrect free-game source default: %v", err)
	}
}

func TestZeroGuildCannotSelectGlobalScope(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	result := New(io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil))).Evaluate(t.Context(), []string{"commands", "--guild", "0", "--confirm"})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "positive Discord snowflake") {
		t.Fatalf("zero guild was not rejected before publication: %v", result.Err)
	}
}
