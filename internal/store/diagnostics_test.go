package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/ent/freedelivery"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/testutil"
)

func TestDiagnosticsQueueAgesAndPoolStatistics(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	now := time.Now()
	for i, status := range []freedelivery.Status{freedelivery.StatusPending, freedelivery.StatusRetry, freedelivery.StatusSending, freedelivery.StatusDead, freedelivery.StatusSent} {
		_, err := db.Client.FreeDelivery.Create().SetOfferID("offer").SetSubscriptionID(string(rune('a' + i))).SetDestinationKind(freedelivery.DestinationKindDm).SetDestinationID("private-destination").SetPayload(freegames.Offer{}).SetStatus(status).SetCreatedAt(now.Add(-time.Duration(i+1) * time.Hour)).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	values, err := db.Diagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"free_pending", "free_retry", "free_sending", "free_dead"} {
		if values[name] != 1 {
			t.Fatalf("%s = %v", name, values[name])
		}
	}
	if age := values["free_oldest_seconds"]; age < 3*time.Hour.Seconds() || age > 3*time.Hour.Seconds()+10 {
		t.Fatalf("queue age includes terminal deliveries: %v", age)
	}

	if values["sale_pending"] != 0 || values["sale_oldest_seconds"] != 0 || values["db_open"] == 0 {
		t.Fatalf("incorrect empty queue or pool statistics: %v", values)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	partial, err := db.Diagnostics(canceled)
	if err == nil {
		t.Fatal("canceled diagnostic query succeeded")
	}

	if _, ok := partial["free_pending"]; ok {
		t.Fatal("failed diagnostic query reported a healthy empty queue")
	}
}
