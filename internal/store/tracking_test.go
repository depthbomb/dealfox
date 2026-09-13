package store_test

import (
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/dealfox/internal/testutil"
)

func TestTrackedGameCount(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	check := func(want int) {
		t.Helper()
		count, err := db.TrackedGameCount(ctx)
		if err != nil || count != want {
			t.Fatalf("tracked games = %d, %v; want %d", count, err, want)
		}
	}
	check(0)
	price := testutil.Price(10, 2000, time.Now())
	add(t, db, "one", "one", price, false, nil)
	add(t, db, "two", "two", price, true, nil)
	price.Country = "GB"
	add(t, db, "three", "three", price, false, nil)
	check(1)
	// Completed one-offs and catalog entries have no enabled tracking rules.
	add(t, db, "four", "four", testutil.Price(20, 1000, time.Now()), false, nil)
	testutil.Must(db.Client.App.Create().SetID(30).SetName("Catalog only").SetType("game").Save(ctx))
	check(1)
	// Recurring rules remain tracked while latched after a sale notification.
	add(t, db, "five", "five", testutil.Price(40, 1000, time.Now()), true, nil)
	check(2)
	testutil.Must(db.Client.Rule.Update().Where(models.RuleColumns.OwnerID.Eq("one")).SetEnabled(false).Exec(ctx))
	check(2)
	testutil.Must(db.Client.Rule.Update().AllRows().SetEnabled(false).Exec(ctx))
	check(0)
}
