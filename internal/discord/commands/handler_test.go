package commands

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo"
	"github.com/depthbomb/tomogo/api"
	tomogocommands "github.com/depthbomb/tomogo/commands"
	"github.com/depthbomb/tomogo/testkit"
)

type steamStub struct {
	price domain.Price
	err   error
}

func (s steamStub) Details(context.Context, int64, string) (domain.Price, error) {
	return s.price, s.err
}

func (s steamStub) Artwork(context.Context, int64, string) (string, error) {
	return s.price.CapsuleURL, s.err
}

func (s steamStub) Prices(context.Context, []int64, string) (map[int64]domain.Price, error) {
	return nil, nil
}

func (s steamStub) Catalog(context.Context, func(context.Context, []domain.App) error) error {
	return nil
}

func newTestHandler(t *testing.T, service *tracker.Service) (*Handler, *tomogo.App) {
	t.Helper()
	b := &Handler{
		Tracker: service,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	app, err := tomogo.New(tomogo.Config{
		Token:                   "test-token",
		InteractionErrorHandler: b.HandleInteractionError,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Register(app); err != nil {
		t.Fatal(err)
	}

	return b, app
}

func dispatch(t *testing.T, app *tomogo.App, name string, options ...api.InteractionOption) *testkit.InteractionHarness {
	t.Helper()
	i := testkit.CommandInteraction(name, options...)
	i.User = &api.User{
		ID: 123,
	}
	harness := testkit.NewInteractionHarness(i)
	if err := app.DispatchInteraction(t.Context(), i, harness.Responder); err != nil {
		t.Fatal(err)
	}

	initial := harness.InitialResponses()
	if len(initial) != 1 {
		t.Fatal("expected exactly one initial response")
	}

	wantEphemeral := name != "about"
	if ephemeral := initial[0].Response.Data.Flags&api.MessageFlagEphemeral != 0; ephemeral != wantEphemeral {
		t.Fatal("command response visibility changed")
	}

	if initial[0].Response.Type == api.CallbackDeferredChannelMessage {
		if len(harness.OriginalEdits()) != 1 || harness.OriginalEdits()[0].Embeds == nil {
			t.Fatal("deferred response was not completed with an embed")
		}
		assertNoMentions(t, harness.OriginalEdits()[0].AllowedMentions)
	} else if initial[0].Response.Type != api.CallbackChannelMessage || len(initial[0].Response.Data.Embeds) == 0 || len(harness.OriginalEdits()) != 0 {
		t.Fatal("immediate response was not sent with an embed")
	} else {
		assertNoMentions(t, initial[0].Response.Data.AllowedMentions)
	}

	return harness
}

func TestOwner(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interaction *api.Interaction
		want        string
	}{
		{
			name: "nil interaction",
		},
		{
			name:        "missing actor",
			interaction: &api.Interaction{},
		},
		{
			name: "DM user",
			interaction: &api.Interaction{
				User: &api.User{
					ID: 123,
				},
			},
			want: "123",
		},
		{
			name: "guild member takes precedence",
			interaction: &api.Interaction{
				Member: &api.GuildMember{
					User: &api.User{
						ID: 456,
					},
				},
				User: &api.User{
					ID: 123,
				},
			},
			want: "456",
		},
		{
			name: "invalid member does not fall back to another user",
			interaction: &api.Interaction{
				Member: &api.GuildMember{
					User: &api.User{},
				},
				User: &api.User{
					ID: 123,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := owner(tc.interaction)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("owner = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestCommandContract(t *testing.T) {
	b, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
	})
	definitions, err := b.Definitions()
	if err != nil {
		t.Fatal(err)
	}

	if len(definitions) != 5 {
		t.Fatalf("expected price, track, freegames, account, and about; got %d commands", len(definitions))
	}

	for _, def := range definitions {
		if err := tomogocommands.Validate(def); err != nil {
			t.Fatal(err)
		}

		if len(def.IntegrationTypes) != 2 || len(def.Contexts) != 3 {
			t.Fatalf("unexpected command contract: %+v", def)
		}
	}
	dispatch(t, app, "about")
	b.Tracker.Config.NewTracksEnabled = false
	dispatch(t, app, "track", testkit.Subcommand("add"))
}

func TestCommandWorkflows(t *testing.T) {
	db, _ := testutil.Database(t)
	b, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
		Store:  db,
		Steam: steamStub{
			price: testutil.Price(10, 2000, time.Now()),
		},
	})
	dispatch(t, app, "price", testkit.StringOption("game", "10"))
	dispatch(t, app, "track", testkit.Subcommand("add", testkit.StringOption("game", "10"), testkit.StringOption("condition", domain.AnySale)))
	rules, err := db.List(t.Context(), "123")
	if err != nil || len(rules) != 1 {
		t.Fatalf("command did not persist rule: %v", err)
	}

	listed := dispatch(t, app, "track", testkit.Subcommand("list"))
	encoded, _ := json.Marshal(listed.OriginalEdits())
	if !strings.Contains(string(encoded), rules[0].ID) {
		t.Fatal("list did not include removable rule ID")
	}

	dispatch(t, app, "track", testkit.Subcommand("remove", testkit.StringOption("id", rules[0].ID)))
	rules, err = db.List(t.Context(), "123")
	if err != nil || len(rules) != 0 {
		t.Fatal("remove did not disable the user's rule")
	}

	b.Tracker.Steam = steamStub{
		price: testutil.Price(20, 0, time.Now()),
	}
	dispatch(t, app, "price", testkit.StringOption("game", "20"))
}

func TestLimiter(t *testing.T) {
	var l limiter
	now := time.Now()
	for range 2 {
		if err := l.allow("user", 2, time.Minute, now); err != nil {
			t.Fatal(err)
		}
	}

	if err := l.allow("user", 2, time.Minute, now); err == nil {
		t.Fatal("rate limit was exceeded")
	}

	if err := l.allow("user", 2, time.Minute, now.Add(time.Minute)); err != nil {
		t.Fatal("window failed to reset")
	}
}
