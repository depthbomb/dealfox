package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/delivery"
	"github.com/depthbomb/dealfox/ent/freedelivery"
	"github.com/depthbomb/dealfox/ent/freesubscription"
	"github.com/depthbomb/dealfox/ent/rule"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/testutil"
)

func seedAccounts(t *testing.T, db *store.Store) {
	t.Helper()
	for _, owner := range []string{"123", "999"} {
		for id := int64(1); id <= 2; id++ {
			_, err := db.Add(t.Context(), store.AddRequest{
				OwnerID:   owner,
				Price:     testutil.Price(id, 1000, time.Now()),
				Condition: domain.AnySale,
				Recurring: id == 2,
				Maximum:   25,
				Interval:  time.Hour,
			})
			if err != nil {
				t.Fatal(err)
			}
		}

		if err := db.SetFreeSubscriptions(t.Context(), "dm:"+owner, owner, owner, []string{"gog", "epic"}, true); err != nil {
			t.Fatal(err)
		}

		if err := db.SetFreeSubscriptions(t.Context(), "dm:"+owner, owner, owner, []string{"epic"}, false); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.SetFreeSubscriptions(t.Context(), "guild:456", "789", "123", []string{"gog"}, true); err != nil {
		t.Fatal(err)
	}

	if err := db.InitializeFreeMonitors(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := db.RecordFreeScan(t.Context(), "gog", freegames.Result{
		Offers: []freegames.Offer{freeOffer()},
	}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAccountIsolationAndHistory(t *testing.T) {
	db, _ := testutil.Database(t)
	seedAccounts(t, db)
	ctx := t.Context()
	if db.Client.Delivery.Query().Where(delivery.DestinationIDEQ("123")).CountX(ctx) != 2 || db.Client.FreeDelivery.Query().Where(freedelivery.DestinationIDEQ("123")).CountX(ctx) != 1 {
		t.Fatal("fixture did not create personal notification history")
	}

	if err := db.DeleteAccount(ctx, "123"); err != nil {
		t.Fatal(err)
	}

	if db.Client.Rule.Query().Where(rule.OwnerIDEQ("123")).CountX(ctx) != 0 || db.Client.Delivery.Query().Where(delivery.DestinationIDEQ("123")).CountX(ctx) != 0 || db.Client.FreeDelivery.Query().Where(freedelivery.DestinationIDEQ("123")).CountX(ctx) != 0 || db.Client.FreeSubscription.Query().Where(freesubscription.Or(freesubscription.ScopeEQ("dm:123"), freesubscription.ManagedByEQ("123"))).CountX(ctx) != 0 {
		t.Fatal("personal data survived deletion")
	}

	if db.Client.Rule.Query().Where(rule.OwnerIDEQ("999")).CountX(ctx) != 2 || db.Client.Event.Query().CountX(ctx) != 2 || db.Client.Delivery.Query().CountX(ctx) != 2 || db.Client.FreeSubscription.Query().CountX(ctx) != 3 || db.Client.FreeDelivery.Query().CountX(ctx) != 2 {
		t.Fatal("deletion changed other users or server alerts")
	}

	server := db.Client.FreeSubscription.Query().Where(freesubscription.ScopeEQ("guild:456")).OnlyX(ctx)
	if !server.Enabled || server.ManagedBy == "123" || server.DestinationID != "789" {
		t.Fatal("server alert was not preserved with its manager removed")
	}

	if db.Client.App.Query().CountX(ctx) != 2 || db.Client.Target.Query().CountX(ctx) != 2 || db.Client.FreeOffer.Query().CountX(ctx) != 1 {
		t.Fatal("shared game data was deleted")
	}

	if err := db.DeleteAccount(ctx, "123"); err != nil {
		t.Fatalf("repeated deletion should be harmless: %v", err)
	}
}

func TestDeleteAccountRollsBackAllPersonalChanges(t *testing.T) {
	db, _ := testutil.Database(t)
	seedAccounts(t, db)
	failure := errors.New("test deletion failure")
	db.Client.FreeSubscription.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if mutation.Op().Is(ent.OpDelete) {
				return nil, failure
			}

			return next.Mutate(ctx, mutation)
		})
	})
	if err := db.DeleteAccount(t.Context(), "123"); !errors.Is(err, failure) {
		t.Fatalf("expected injected transaction failure: %v", err)
	}

	ctx := t.Context()
	if db.Client.Rule.Query().CountX(ctx) != 4 || db.Client.Event.Query().CountX(ctx) != 4 || db.Client.Delivery.Query().CountX(ctx) != 4 || db.Client.FreeSubscription.Query().CountX(ctx) != 5 || db.Client.FreeDelivery.Query().CountX(ctx) != 3 {
		t.Fatal("failed account deletion committed partial changes")
	}
}

func TestDeleteAccountWaitsForDeliveryAndHonorsCancellation(t *testing.T) {
	db, _ := testutil.Database(t)
	seedAccounts(t, db)
	release, err := db.BeginDelivery(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	err = db.DeleteAccount(ctx, "123")
	release()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deletion passed an active delivery: %v", err)
	}

	if db.Client.Rule.Query().Where(rule.OwnerIDEQ("123")).CountX(t.Context()) != 2 {
		t.Fatal("cancelled deletion removed data")
	}

	if err := db.DeleteAccount(t.Context(), "123"); err != nil {
		t.Fatal(err)
	}
}
