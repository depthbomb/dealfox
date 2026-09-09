package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/preconditions"
	"github.com/depthbomb/tomogo/testkit"
)

func TestCooldownCapacityIsOperationalAndDoesNotEvictQuota(t *testing.T) {
	h, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
	})
	cooldown, err := preconditions.NewCooldown(preconditions.CooldownConfig{
		Limit:   1,
		Window:  time.Minute,
		MaxKeys: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.cooldowns["capacity-test"] = cooldown
	check := h.rateLimit("capacity-test", 1, time.Minute)
	if err := app.Commands().Register(api.ApplicationCommand{
		Name:        "capacity",
		Description: "Test bounded admission",
	}, func(ctx context.Context, i *api.Interaction, _ *interactions.Responder) error {
		return check.Check(ctx, i)
	}); err != nil {
		t.Fatal(err)
	}
	if err := cooldown.Allow(t.Context(), "123"); err != nil {
		t.Fatal(err)
	}
	denied := invokeLimitedCommand(t, app, 999, 0, "capacity")
	message := *denied.InitialResponses()[0].Response.Data.Embeds[0].Description
	if strings.Contains(message, "Please try again in ") || !strings.Contains(message, "Reference:") {
		t.Fatal("capacity was presented as a user's exhausted quota")
	}

	if err := cooldown.Allow(t.Context(), "123"); !errors.Is(err, preconditions.ErrDenied) {
		t.Fatal("capacity refusal evicted a live allowance")
	}

	if _, public := commandErrorMessage(preconditions.ErrCooldownCapacity); public {
		t.Fatal("capacity error exposed fabricated retry text")
	}
}

func TestFreeGameCooldownIsSharedWithoutDatabase(t *testing.T) {
	cfg := testutil.Config(t)
	cfg.CommandRateLimit = 100
	h, app := newTestHandler(t, &tracker.Service{
		Config: cfg,
	})
	// Accessors and account locking are outside this test. Inspect the exact
	// checks installed by the shared command-definition constructors.
	command := h.freeGamesCommand()
	first := command.subcommands[0].preconditions[0]
	second := command.subcommands[1].preconditions[0]
	interaction := accountInteraction()
	for index := range 10 {
		check := first
		if index%2 != 0 {
			check = second
		}
		if err := check.Check(t.Context(), interaction); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"subscribe", "unsubscribe"} {
		requireRateDenial(t, invokeLimitedCommand(t, app, 123, 456, "freegames", testkit.Subcommand(name)))
	}
	if err := h.rateLimit("price", cfg.PriceRateLimit, cfg.PriceRateWindow).Check(t.Context(), interaction); err != nil {
		t.Fatal("freegames consumed the independent price bucket", err)
	}
}

func TestCooldownPublicRetryRounding(t *testing.T) {
	for _, test := range []struct {
		delay time.Duration
		want  string
	}{
		{
			delay: time.Nanosecond,
			want:  "Please try again in 1 second.",
		},
		{
			delay: time.Second,
			want:  "Please try again in 1 second.",
		},
		{
			delay: time.Second + time.Nanosecond,
			want:  "Please try again in 2 seconds.",
		},
	} {
		message, public := commandErrorMessage(&preconditions.CooldownError{
			RetryAfter: test.delay,
		})
		if !public || message != test.want {
			t.Fatalf("retry %s: %q", test.delay, message)
		}
	}
}
