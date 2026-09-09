package commands

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	tomogocommands "github.com/depthbomb/tomogo/commands"
	"github.com/depthbomb/tomogo/testkit"
)

type argumentSteam struct {
	steamStub
	country string
	calls   int
}

func (s *argumentSteam) Details(_ context.Context, id int64, country string) (domain.Price, error) {
	s.calls++
	s.country = country
	price := testutil.Price(id, 2000, time.Now())
	price.Country = country

	return price, nil
}

func TestPriceNamedArgumentsPreserveDefaultsAndRejectMalformedValues(t *testing.T) {
	for _, test := range []struct {
		name      string
		country   string
		malformed bool
		want      string
	}{
		{
			name: "default",
			want: "CA",
		},
		{
			name:    "explicit",
			country: "GB",
			want:    "GB",
		},
		{
			name:      "invalid type",
			malformed: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := testutil.Config(t)
			cfg.DefaultCountry = "CA"
			steam := &argumentSteam{}
			h, app := newTestHandler(t, &tracker.Service{
				Config: cfg,
				Steam:  steam,
			})
			var execution tomogocommands.Execution
			app.Commands().SetObserver(func(ctx context.Context, result tomogocommands.Execution) {
				execution = result
				h.observeCommand(ctx, result)
			})
			options := []api.InteractionOption{testkit.StringOption("game", "10")}
			if test.country != "" {
				options = append(options, testkit.StringOption("country", test.country))
			}

			if test.malformed {
				options = append(options, testkit.BooleanOption("country", true))
			}
			dispatch(t, app, "price", options...)
			if test.malformed {
				if steam.calls != 0 || !errors.Is(execution.Err, tomogocommands.ErrOptionType) {
					t.Fatalf("malformed country used a default: calls=%d error=%v", steam.calls, execution.Err)
				}
			} else if steam.calls != 1 || steam.country != test.want || execution.Err != nil {
				t.Fatalf("named country = %q, calls=%d, error=%v", steam.country, steam.calls, execution.Err)
			}
		})
	}
}

func TestTrackNamedArgumentsPreserveBudgetAndRecurring(t *testing.T) {
	db, _ := testutil.Database(t)
	_, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
		Store:  db,
		Steam:  &argumentSteam{},
	})
	dispatch(t, app, "track", testkit.Subcommand("add",
		testkit.StringOption("game", "10"),
		testkit.StringOption("condition", domain.UnderBudget),
		testkit.StringOption("budget", "19.99 USD"),
		testkit.BooleanOption("recurring", true),
	))
	rules, err := db.List(t.Context(), "123")
	if err != nil || len(rules) != 1 {
		t.Fatalf("track add failed: %v, %v", rules, err)
	}
	added := rules[0]
	if !added.Recurring || added.BudgetMinor == nil || *added.BudgetMinor != 1999 || added.BudgetCurrency != "USD" || added.Edges.Target.Country != "US" {
		t.Fatal("track arguments lost budget, recurrence, or default country")
	}
}
