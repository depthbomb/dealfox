package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/nook"
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
	if testutil.Must(db.Client.Delivery.Query().Where(models.DeliveryColumns.DestinationID.Eq("123")).Count(ctx)) != 2 || testutil.Must(db.Client.FreeDelivery.Query().Where(models.FreeDeliveryColumns.DestinationID.Eq("123")).Count(ctx)) != 1 {
		t.Fatal("fixture did not create personal notification history")
	}

	if err := db.DeleteAccount(ctx, "123"); err != nil {
		t.Fatal(err)
	}

	if testutil.Must(db.Client.Rule.Query().Where(models.RuleColumns.OwnerID.Eq("123")).Count(ctx)) != 0 || testutil.Must(db.Client.Delivery.Query().Where(models.DeliveryColumns.DestinationID.Eq("123")).Count(ctx)) != 0 || testutil.Must(db.Client.FreeDelivery.Query().Where(models.FreeDeliveryColumns.DestinationID.Eq("123")).Count(ctx)) != 0 || testutil.Must(db.Client.FreeSubscription.Query().Where(nook.Or(models.FreeSubscriptionColumns.Scope.Eq("dm:123"), models.FreeSubscriptionColumns.ManagedBy.Eq("123"))).Count(ctx)) != 0 {
		t.Fatal("personal data survived deletion")
	}

	if testutil.Must(db.Client.Rule.Query().Where(models.RuleColumns.OwnerID.Eq("999")).Count(ctx)) != 2 || testutil.Must(db.Client.Event.Query().Count(ctx)) != 2 || testutil.Must(db.Client.Delivery.Query().Count(ctx)) != 2 || testutil.Must(db.Client.FreeSubscription.Query().Count(ctx)) != 3 || testutil.Must(db.Client.FreeDelivery.Query().Count(ctx)) != 2 {
		t.Fatal("deletion changed other users or server alerts")
	}

	server := testutil.Must(db.Client.FreeSubscription.Query().Where(models.FreeSubscriptionColumns.Scope.Eq("guild:456")).Only(ctx))
	if !server.Enabled || server.ManagedBy == "123" || server.DestinationID != "789" {
		t.Fatal("server alert was not preserved with its manager removed")
	}

	if testutil.Must(db.Client.App.Query().Count(ctx)) != 2 || testutil.Must(db.Client.Target.Query().Count(ctx)) != 2 || testutil.Must(db.Client.FreeOffer.Query().Count(ctx)) != 1 {
		t.Fatal("shared game data was deleted")
	}

	if err := db.DeleteAccount(ctx, "123"); err != nil {
		t.Fatalf("repeated deletion should be harmless: %v", err)
	}
}

func TestDeleteAccountRollsBackAllPersonalChanges(t *testing.T) {
	db, _ := testutil.Database(t)
	seedAccounts(t, db)
	if _, err := db.Client.SQL().ExecContext(t.Context(), "CREATE TRIGGER fail_account_delete BEFORE DELETE ON free_subscriptions BEGIN SELECT RAISE(ABORT, 'test deletion failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAccount(t.Context(), "123"); err == nil || !strings.Contains(err.Error(), "test deletion failure") {
		t.Fatalf("expected injected transaction failure: %v", err)
	}

	ctx := t.Context()
	if testutil.Must(db.Client.Rule.Query().Count(ctx)) != 4 || testutil.Must(db.Client.Event.Query().Count(ctx)) != 4 || testutil.Must(db.Client.Delivery.Query().Count(ctx)) != 4 || testutil.Must(db.Client.FreeSubscription.Query().Count(ctx)) != 5 || testutil.Must(db.Client.FreeDelivery.Query().Count(ctx)) != 3 {
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

	if testutil.Must(db.Client.Rule.Query().Where(models.RuleColumns.OwnerID.Eq("123")).Count(t.Context())) != 2 {
		t.Fatal("cancelled deletion removed data")
	}

	if err := db.DeleteAccount(t.Context(), "123"); err != nil {
		t.Fatal(err)
	}
}
