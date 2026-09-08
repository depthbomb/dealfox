package tracker

import (
	"context"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/testutil"
)

type fakeSteam struct {
	price     domain.Price
	requested int64
}

func (s *fakeSteam) Details(_ context.Context, id int64, country string) (domain.Price, error) {
	s.requested = id
	p := s.price
	p.AppID = id
	p.Country = country

	return p, nil
}

func (s *fakeSteam) Prices(context.Context, []int64, string) (map[int64]domain.Price, error) {
	return nil, nil
}

func (s *fakeSteam) Catalog(context.Context, func(context.Context, []domain.App) error) error {
	return nil
}

func TestResolutionAndTrackingValidation(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	apps := []domain.App{
		{
			ID:   1,
			Name: "Portal",
			Type: "game",
		},
		{
			ID:   2,
			Name: "Portal 2",
			Type: "game",
		},
		{
			ID:   3,
			Name: "Unique DLC",
			Type: "dlc",
		},
		{
			ID:   4,
			Name: "100% literal_",
			Type: "game",
		},
	}
	if err := db.UpsertCatalog(ctx, apps); err != nil {
		t.Fatal(err)
	}

	steam := &fakeSteam{
		price: testutil.Price(1, 1000, time.Now()),
	}
	s := &Service{
		Store:  db,
		Steam:  steam,
		Config: testutil.Config(t),
	}
	for input, expected := range map[string]int64{
		"portal":                                1,
		"Unique":                                3,
		"https://store.steampowered.com/app/10": 10,
		"% literal_":                            4,
	} {
		if _, err := s.Price(ctx, input, "us"); err != nil || steam.requested != expected {
			t.Fatalf("resolve %q: id=%d, err=%v", input, steam.requested, err)
		}
	}

	for _, input := range []string{"Port", "no match"} {
		if _, err := s.Price(ctx, input, "US"); err == nil {
			t.Fatalf("ambiguous or missing name accepted: %s", input)
		}
	}

	for _, kind := range []string{"bundle", "software"} {
		steam.price.Type = kind
		if _, err := s.Price(ctx, "10", "US"); err == nil {
			t.Fatalf("untrackable type accepted: %s", kind)
		}
	}

	steam.price.Type = "game"
	steam.price.Availability = "free"
	if _, err := s.Price(ctx, "10", "US"); err == nil {
		t.Fatal("free-to-play app accepted")
	}

	steam.price.Availability = "priced"
	for _, budget := range []string{"10.00 EUR", "invalid"} {
		if _, _, err := s.Add(ctx, AddRequest{
			OwnerID:   "owner",
			Game:      "10",
			Country:   "US",
			Condition: domain.UnderBudget,
			Budget:    budget,
		}); err == nil {
			t.Fatalf("invalid budget accepted: %s", budget)
		}
	}
}

func TestUserLockCancellationAndCleanup(t *testing.T) {
	var locks userLocks
	release, err := locks.acquire(t.Context(), "one")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := locks.acquire(ctx, "one"); err == nil {
		t.Fatal("cancelled lock wait succeeded")
	}

	other, err := locks.acquire(t.Context(), "two")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	if len(locks.entries) != 0 {
		t.Fatal("idle user locks leaked")
	}
}
