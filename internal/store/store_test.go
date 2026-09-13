package store_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/dealfox/internal/testutil"
)

func add(t *testing.T, db *store.Store, owner, request string, p domain.Price, recurring bool, budget *domain.Money) *models.Rule {
	t.Helper()
	condition := domain.AnySale
	if budget != nil {
		condition = domain.UnderBudget
	}

	r, err := db.Add(t.Context(), store.AddRequest{
		OwnerID:   owner,
		RequestID: request,
		Price:     p,
		Condition: condition,
		Budget:    budget,
		Recurring: recurring,
		Maximum:   25,
		Interval:  time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	return r
}

func TestSaleLifecycle(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	at := time.Now().UTC().Add(-time.Hour)
	p := testutil.Price(10, 1000, at)
	oneOff := add(t, db, "one", "request-one", p, false, nil)
	if oneOff.Enabled {
		t.Fatal("initial qualification did not complete one-off rule")
	}

	replay := add(t, db, "one", "request-one", p, false, nil)
	if replay.ID != oneOff.ID || testutil.Must(db.Client.Delivery.Query().Count(ctx)) != 1 {
		t.Fatal("replayed request duplicated the rule or notification")
	}

	budget := &domain.Money{
		Minor:    1000,
		Currency: "USD",
	}
	recurring := add(t, db, "recurring", "request-recurring", p, true, budget)
	if !recurring.Enabled || !recurring.Latched {
		t.Fatal("initial recurring qualification was not latched")
	}

	if recurring.TargetID != oneOff.TargetID {
		t.Fatal("same app and country did not share a target")
	}

	steps := []struct {
		name     string
		final    int64
		currency string
		unknown  bool
		want     int
	}{
		{
			name:     "same sale",
			final:    1000,
			currency: "USD",
			want:     2,
		},
		{
			name:     "above budget",
			final:    1500,
			currency: "USD",
			want:     2,
		},
		{
			name:     "back under budget",
			final:    800,
			currency: "USD",
			want:     2,
		},
		{
			name:    "missing",
			unknown: true,
			want:    2,
		},
		{
			name:     "wrong currency off sale",
			final:    2000,
			currency: "EUR",
			want:     2,
		},
		{
			name:     "still same sale",
			final:    800,
			currency: "USD",
			want:     2,
		},
		{
			name:     "off sale rearms",
			final:    2000,
			currency: "USD",
			want:     2,
		},
		{
			name:     "new sale",
			final:    900,
			currency: "USD",
			want:     3,
		},
	}
	for index, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			observation := testutil.Price(10, step.final, at.Add(time.Duration(index+1)*time.Minute))
			observation.Currency = step.currency
			if step.unknown {
				observation.Availability = "unknown"
				observation.SaleState = "unknown"
			}

			if err := db.Observe(ctx, recurring.TargetID, observation, time.Hour); err != nil {
				t.Fatal(err)
			}

			if count := testutil.Must(db.Client.Delivery.Query().Count(ctx)); count != int64(step.want) {
				t.Fatalf("got %d deliveries, want %d", count, step.want)
			}
		})
	}

	stale := testutil.Price(10, 2000, at.Add(time.Minute))
	if err := db.Observe(ctx, recurring.TargetID, stale, time.Hour); err != nil {
		t.Fatal(err)
	}

	add(t, db, "late", "late", stale, true, nil)
	if !testutil.Must(db.Client.Rule.Get(ctx, recurring.ID)).Latched {
		t.Fatal("stale cached creation data regressed another rule's latch")
	}

	if err := db.Remove(ctx, "stranger", recurring.ID); err == nil {
		t.Fatal("a stranger removed an owned rule")
	}

	if err := db.Remove(ctx, "recurring", recurring.ID); err != nil {
		t.Fatal(err)
	}

	if testutil.Must(db.Client.Rule.Get(ctx, recurring.ID)).Enabled {
		t.Fatal("removed rule remained enabled")
	}
}

func TestTransactionsQuotaAndClaims(t *testing.T) {
	db, dsn := testutil.Database(t)
	ctx := t.Context()
	at := time.Now().Add(-2 * time.Hour)
	var group sync.WaitGroup
	results := make(chan error, 12)
	for index := range 12 {
		group.Go(func() {
			_, err := db.Add(ctx, store.AddRequest{
				OwnerID:   "quota",
				Price:     testutil.Price(int64(100+index), 2000, at),
				Condition: domain.AnySale,
				Maximum:   3,
				Interval:  time.Hour,
			})
			results <- err
		})
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			var public *domain.PublicError
			if !errors.As(err, &public) || public.Code != "QUOTA_EXCEEDED" {
				t.Fatal(err)
			}
		}
	}

	if successes != 3 {
		t.Fatalf("quota admitted %d rules", successes)
	}

	before := testutil.Must(db.Client.Observation.Query().Count(ctx))
	incomplete := testutil.Price(500, 1000, at)
	incomplete.Regular = nil
	_, err := db.Add(ctx, store.AddRequest{
		OwnerID:   "rollback",
		Price:     incomplete,
		Condition: domain.AnySale,
		Maximum:   25,
		Interval:  time.Hour,
	})
	if err == nil || testutil.Must(db.Client.Observation.Query().Count(ctx)) != before || testutil.Must(db.Client.Rule.Query().Where(models.RuleColumns.OwnerID.Eq("rollback")).Count(ctx)) != 0 {
		t.Fatal("failed notification creation did not roll back atomically")
	}

	add(t, db, "delivery", "delivery", testutil.Price(600, 1000, at), false, nil)
	other, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	claims := make(chan *models.Delivery, 2)
	errors := make(chan error, 2)
	for _, connection := range []*store.Store{db, other} {
		group.Go(func() {
			d, err := connection.Claim(ctx)
			claims <- d
			errors <- err
		})
	}
	group.Wait()
	close(claims)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}

	claimed := 0
	for d := range claims {
		if d != nil {
			claimed++
		}
	}

	if claimed != 1 {
		t.Fatalf("concurrent workers claimed %d deliveries", claimed)
	}

	if err := db.Recover(ctx, true); err != nil {
		t.Fatal(err)
	}

	d, err := db.Claim(ctx)
	if err != nil || d == nil || d.AttemptCount != 2 {
		t.Fatalf("recovery: %+v, %v", d, err)
	}

	if err := db.Finish(ctx, d.ID, models.DeliveryStatusDead, time.Now(), "", "test failure"); err != nil {
		t.Fatal(err)
	}

	if err := db.Administer(ctx, d.ID, true); err != nil {
		t.Fatal(err)
	}

	if err := db.Administer(ctx, d.ID, false); err != nil {
		t.Fatal(err)
	}

	if err := db.Administer(ctx, d.ID, true); err == nil {
		t.Fatal("cancelled delivery was retried")
	}

	old := time.Now().Add(-100 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000000000Z")
	for _, statement := range []string{
		"UPDATE deliveries SET updated_at = ?",
		"UPDATE events SET created_at = ?",
		"UPDATE rules SET updated_at = ? WHERE NOT enabled",
	} {
		if _, err := db.Client.SQL().ExecContext(ctx, statement, old); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.Purge(ctx, time.Nanosecond, time.Hour, time.Hour); err != nil {
		t.Fatal(err)
	}

	if testutil.Must(db.Client.Delivery.Query().Count(ctx)) != 0 || testutil.Must(db.Client.Event.Query().Count(ctx)) != 0 || testutil.Must(db.Client.Rule.Query().Where(models.RuleColumns.OwnerID.Eq("delivery")).Count(ctx)) != 0 {
		t.Fatal("retention failed to purge terminal dependencies")
	}

	unlock, err := db.LockProcess(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if release, err := other.LockProcess(ctx); err == nil {
		release()
		t.Fatal("second serve process was admitted")
	}
}

func TestMigrationValidation(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	if _, err := db.Client.SQL().ExecContext(ctx, "UPDATE nook_migrations SET checksum = 'tampered'"); err != nil {
		t.Fatal(err)
	}

	if err := db.ValidateMigrations(ctx); err == nil {
		t.Fatal("changed migration history was accepted")
	}
}
