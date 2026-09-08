package store_test

import (
	"testing"
	"time"

	"github.com/depthbomb/dealfox/ent/freedelivery"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/testutil"
)

func freeOffer() freegames.Offer {
	return freegames.Offer{
		Source:      "gog",
		ProductID:   "123",
		Campaign:    "2026-test",
		Country:     "US",
		Platform:    "PC",
		Title:       "Test game",
		Description: "A complete game.",
		URL:         "https://www.gog.com/en/game/test",
		ImageURL:    "https://images.gog-statics.com/test.jpg",
		ObservedAt:  time.Now(),
		EndsAt:      time.Now().Add(time.Hour),
		Eligible:    true,
		Evidence: []freegames.Evidence{{
			URL:  "https://www.gog.com/en/game/test",
			Hash: "verified",
		}},
	}
}

func TestFreeSubscriptionsOutboxAndIsolation(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	if err := db.InitializeFreeMonitors(ctx); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []struct {
		scope       string
		destination string
		sources     []string
	}{
		{
			scope:       "dm:123",
			destination: "123",
			sources:     []string{"gog", "epic"},
		},
		{
			scope:       "guild:456",
			destination: "789",
			sources:     []string{"gog"},
		},
		{
			scope:       "dm:999",
			destination: "999",
			sources:     []string{"steam"},
		},
	} {
		if err := db.SetFreeSubscriptions(ctx, sub.scope, sub.destination, "123", sub.sources, true); err != nil {
			t.Fatal(err)
		}
	}
	offer := freeOffer()
	for range 2 {
		if err := db.RecordFreeScan(ctx, "gog", freegames.Result{
			Offers: []freegames.Offer{offer},
		}, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	count, err := db.Client.FreeDelivery.Query().Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("duplicate scan or source isolation failed: %d %v", count, err)
	}

	if err := db.SetFreeSubscriptions(ctx, "guild:456", "888", "456", []string{"epic"}, true); err != nil {
		t.Fatal(err)
	}
	server, err := db.FreeSubscriptions(ctx, "guild:456")
	if err != nil || len(server) != 2 {
		t.Fatalf("additive sources failed: %+v %v", server, err)
	}
	for _, sub := range server {
		if sub.DestinationID != "888" {
			t.Fatal("server retained multiple destination channels")
		}
	}

	if err := db.SetFreeSubscriptions(ctx, "dm:123", "123", "123", []string{"gog"}, false); err != nil {
		t.Fatal(err)
	}
	delivery, err := db.ClaimFree(ctx)
	if err != nil || delivery == nil || delivery.DestinationID != "888" || delivery.DestinationKind != freedelivery.DestinationKindChannel {
		t.Fatalf("unsubscribe or channel move failed: %+v %v", delivery, err)
	}

	if err := db.RecoverFree(ctx, true); err != nil {
		t.Fatal(err)
	}
	delivery, err = db.ClaimFree(ctx)
	if err != nil || delivery == nil || delivery.AttemptCount != 2 {
		t.Fatalf("restart recovery failed: %+v %v", delivery, err)
	}

	if err := db.FinishFree(ctx, delivery.ID, freedelivery.StatusSent, time.Now(), "message", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFreeScan(ctx, "gog", freegames.Result{
		Offers: []freegames.Offer{offer},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if next, err := db.ClaimFree(ctx); err != nil || next != nil {
		t.Fatalf("sent or cancelled offer replayed: %+v %v", next, err)
	}

	if err := db.SetFreeSubscriptions(ctx, "dm:777", "777", "777", []string{"gog"}, true); err != nil {
		t.Fatal(err)
	}
	if next, err := db.ClaimFree(ctx); err != nil || next == nil || next.DestinationID != "777" {
		t.Fatalf("new subscription missed active offer: %+v %v", next, err)
	}
}

func TestFreeScanFailureDoesNotEraseOffersAndExpiryCancels(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	if err := db.InitializeFreeMonitors(ctx); err != nil {
		t.Fatal(err)
	}
	offer := freeOffer()
	if err := db.RecordFreeScan(ctx, "gog", freegames.Result{
		Offers: []freegames.Offer{offer},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFreeScan(ctx, "gog", freegames.Result{
		Problems: []string{"transport failed"},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	saved, err := db.Client.FreeOffer.Query().Only(ctx)
	if err != nil || !saved.Eligible {
		t.Fatalf("outage erased known offer: %+v %v", saved, err)
	}

	if err := db.SetFreeSubscriptions(ctx, "dm:123", "123", "123", []string{"gog"}, true); err != nil {
		t.Fatal(err)
	}
	offer.EndsAt = time.Now().Add(-time.Second)
	if err := db.Client.FreeOffer.UpdateOne(saved).SetPayload(offer).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if next, err := db.ClaimFree(ctx); err != nil || next != nil {
		t.Fatalf("expired promotion delivered: %+v %v", next, err)
	}
	if err := db.RecordFreeScan(ctx, "gog", freegames.Result{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	saved, err = db.Client.FreeOffer.Query().Only(ctx)
	if err != nil || saved.Eligible {
		t.Fatalf("successful empty result did not retire offer: %+v %v", saved, err)
	}

	offer.EndsAt = time.Now().Add(time.Hour)
	if err := db.RecordFreeScan(ctx, "gog", freegames.Result{
		Offers: []freegames.Offer{offer},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if next, err := db.ClaimFree(ctx); err != nil || next == nil || next.OfferGeneration != 2 {
		t.Fatalf("repeat giveaway did not rearm after confirmed absence: %+v %v", next, err)
	}
}
