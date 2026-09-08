package commands

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/preconditions"
	"github.com/depthbomb/tomogo/testkit"
)

func invokeLimitedCommand(t *testing.T, app *tomogo.App, user, guild api.ID, name string, options ...api.InteractionOption) *testkit.InteractionHarness {
	t.Helper()
	i := testkit.CommandInteraction(name, options...)
	i.User = &api.User{
		ID: user,
	}
	i.GuildID = guild
	if guild != 0 {
		i.Member = &api.GuildMember{
			User: i.User,
		}
		i.User = nil
	}
	harness := testkit.NewInteractionHarness(i)
	if err := app.DispatchInteraction(t.Context(), i, harness.Responder); err != nil {
		t.Fatal(err)
	}

	return harness
}

func requireRateDenial(t *testing.T, harness *testkit.InteractionHarness) {
	t.Helper()
	initial := harness.InitialResponses()
	if len(initial) != 1 || initial[0].Response.Type != api.CallbackChannelMessage || initial[0].Response.Data.Flags&api.MessageFlagEphemeral == 0 {
		t.Fatal("rate denial must be one immediate private response")
	}

	embeds := initial[0].Response.Data.Embeds
	if len(embeds) != 1 || embeds[0].Description == nil || !strings.HasPrefix(*embeds[0].Description, "Please try again in ") {
		t.Fatal("rate denial lost its public retry message")
	}

	if len(harness.OriginalEdits()) != 0 || len(harness.Followups()) != 0 {
		t.Fatal("denied command executed its response handler")
	}
}

func TestSharedLimitCoversEveryCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		sub  string
	}{
		{
			name: "about",
		},
		{
			name: "account",
			sub:  "delete",
		},
		{
			name: "price",
		},
		{
			name: "track",
			sub:  "list",
		},
		{
			name: "track",
			sub:  "add",
		},
		{
			name: "track",
			sub:  "remove",
		},
		{
			name: "freegames",
			sub:  "status",
		},
		{
			name: "freegames",
			sub:  "subscribe",
		},
		{
			name: "freegames",
			sub:  "unsubscribe",
		},
	} {
		t.Run(tc.name+"/"+tc.sub, func(t *testing.T) {
			cfg := testutil.Config(t)
			cfg.CommandRateLimit = 1
			_, app := newTestHandler(t, &tracker.Service{
				Config: cfg,
			})
			dispatch(t, app, "about")
			var options []api.InteractionOption
			if tc.sub != "" {
				options = append(options, testkit.Subcommand(tc.sub))
			}
			harness := invokeLimitedCommand(t, app, 123, 456, tc.name, options...)
			requireRateDenial(t, harness)

			other := invokeLimitedCommand(t, app, 789, 456, "about")
			if other.InitialResponses()[0].Response.Data.Flags&api.MessageFlagEphemeral != 0 {
				t.Fatal("one user exhausted another user's allowance")
			}
		})
	}
}

func TestReadCommandsShareAllowanceAcrossContexts(t *testing.T) {
	db, _ := testutil.Database(t)
	cfg := testutil.Config(t)
	cfg.CommandRateLimit = 2
	_, app := newTestHandler(t, &tracker.Service{
		Config: cfg,
		Store:  db,
	})
	for index, name := range []string{"track", "freegames"} {
		sub := "list"
		if name == "freegames" {
			sub = "status"
		}
		harness := invokeLimitedCommand(t, app, 123, api.ID(index*456), name, testkit.Subcommand(sub))
		if len(harness.OriginalEdits()) != 1 {
			t.Fatal("allowed read command did not execute")
		}
	}
	requireRateDenial(t, invokeLimitedCommand(t, app, 123, 999, "track", testkit.Subcommand("list")))
	requireRateDenial(t, invokeLimitedCommand(t, app, 123, 0, "freegames", testkit.Subcommand("status")))
}

func TestSpecificRatePreconditions(t *testing.T) {
	db, _ := testutil.Database(t)
	cfg := testutil.Config(t)
	cfg.CommandRateLimit = 100
	cfg.PriceRateLimit = 1
	cfg.TrackAddRateLimit = 1
	cfg.PriceChecksEnabled = false
	cfg.NewTracksEnabled = false
	_, app := newTestHandler(t, &tracker.Service{
		Config: cfg,
		Store:  db,
	})
	dispatch(t, app, "price")
	requireRateDenial(t, invokeLimitedCommand(t, app, 123, 0, "price"))
	dispatch(t, app, "track", testkit.Subcommand("add"))
	requireRateDenial(t, invokeLimitedCommand(t, app, 123, 0, "track", testkit.Subcommand("add")))
	dispatch(t, app, "track", testkit.Subcommand("list"))

	for index := range 10 {
		sub := "subscribe"
		if index%2 != 0 {
			sub = "unsubscribe"
		}
		dispatch(t, app, "freegames", testkit.Subcommand(sub, testkit.StringOption("sources", "steam")))
	}
	for _, sub := range []string{"subscribe", "unsubscribe"} {
		requireRateDenial(t, invokeLimitedCommand(t, app, 123, 0, "freegames", testkit.Subcommand(sub, testkit.StringOption("sources", "steam"))))
	}
	dispatch(t, app, "freegames", testkit.Subcommand("status"))
}

func TestLimiterConcurrentAdmissionAndExpiry(t *testing.T) {
	var l limiter
	var allowed atomic.Int64
	var group sync.WaitGroup
	now := time.Now()
	for range 100 {
		group.Go(func() {
			err := l.allow("command:123", 20, time.Minute, now)
			if err == nil {
				allowed.Add(1)
			} else if !errors.Is(err, preconditions.ErrDenied) {
				t.Errorf("unexpected limiter error: %v", err)
			}
		})
	}
	group.Wait()
	if allowed.Load() != 20 {
		t.Fatalf("expected 20 admitted commands, got %d", allowed.Load())
	}

	err := l.allow("command:123", 20, time.Minute, now.Add(time.Minute-time.Nanosecond))
	if !errors.Is(err, preconditions.ErrDenied) || err.Error() != "Please try again in 1 second." {
		t.Fatalf("unexpected last-moment denial: %v", err)
	}

	if err := l.allow("command:123", 20, time.Minute, now.Add(time.Minute)); err != nil {
		t.Fatalf("window did not reopen: %v", err)
	}
}
