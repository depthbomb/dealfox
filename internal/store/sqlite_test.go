package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/database"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/nook"
)

func TestSQLiteCatalog(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	apps := []domain.App{
		{
			ID:   10,
			Name: "ÉTÉ Æther",
			Type: "game",
		},
		{
			ID:   20,
			Name: "100%_Complete",
			Type: "dlc",
		},
		{
			ID:   30,
			Name: "100xxComplete",
			Type: "game",
		},
		{
			ID:   40,
			Name: "ÉTÉ Æther",
			Type: "demo",
		},
	}
	if err := db.UpsertCatalog(ctx, apps); err != nil {
		t.Fatal(err)
	}
	for query, id := range map[string]int64{
		"été æ": 10,
		"%_":    20,
	} {
		found, err := db.Search(ctx, query)
		if err != nil || len(found) != 1 || found[0].ID != id {
			t.Fatalf("search %q: %+v, %v", query, found, err)
		}
	}
	if found, err := db.ExactApp(ctx, "été æTHER"); err != nil || found.ID != 10 {
		t.Fatalf("Unicode exact match: %+v, %v", found, err)
	}
	previous := testutil.Must(db.Client.App.Get(ctx, 10))
	apps[0].Name = "Renamed"
	apps[0].LastModified = 123
	if err := db.UpsertCatalog(ctx, apps[:1]); err != nil {
		t.Fatal(err)
	}
	updated := testutil.Must(db.ExactApp(ctx, "RENAMED"))
	if updated.LastModified != 123 || !updated.UpdatedAt.After(previous.UpdatedAt) {
		t.Fatal("upsert lost fields or update timestamp")
	}

	if _, err := db.ExactApp(ctx, "été æther"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("upsert retained old search key", err)
	}

	// A bad row must roll back its entire catalog batch.
	if err := db.UpsertCatalog(ctx, []domain.App{
		{
			ID:   50,
			Name: "valid",
			Type: "game",
		},
		{
			ID:   -1,
			Name: "invalid",
			Type: "game",
		},
	}); err == nil {
		t.Fatal("invalid catalog identity was accepted")
	}

	if _, err := db.Client.App.Get(ctx, 50); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed catalog batch partially committed", err)
	}
}

func TestSQLiteDriftAndProcessLock(t *testing.T) {
	db, path := testutil.Database(t)
	other, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	unlock := testutil.Must(db.LockProcess(t.Context()))
	if release, err := other.LockProcess(t.Context()); err == nil {
		release()
		t.Fatal("second bot admitted")
	}
	unlock()
	release := testutil.Must(other.LockProcess(t.Context()))
	release()
	if _, err := db.Client.SQL().ExecContext(t.Context(), "DROP INDEX rules_owner_id_target_id_condition"); err != nil {
		t.Fatal(err)
	}

	if err := db.ValidateMigrations(t.Context()); err == nil {
		t.Fatal("missing active-rule uniqueness index was accepted")
	}
}

func TestSQLiteConcurrentClaims(t *testing.T) {
	db, path := testutil.Database(t)
	ctx := t.Context()
	for i := range 40 {
		add(t, db, fmt.Sprint(i), fmt.Sprint(i), testutil.Price(int64(100+i), 1000, time.Now()), false, nil)
	}
	other, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var group sync.WaitGroup
	var mutex sync.Mutex
	seen := map[string]bool{}
	for i := range 8 {
		client := []*store.Store{db, other}[i%2]
		group.Go(func() {
			for {
				item, err := client.Claim(ctx)
				if err != nil {
					t.Error(err)
					return
				}

				if item == nil {
					return
				}
				mutex.Lock()
				if seen[item.ID] {
					t.Error("duplicate delivery claim", item.ID)
				}
				seen[item.ID] = true
				mutex.Unlock()
				if err := client.Finish(ctx, item.ID, models.DeliveryStatusSent, time.Now(), "test", ""); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	group.Wait()
	if len(seen) != 40 || testutil.Must(db.Client.Delivery.Query().Where(models.DeliveryColumns.Status.Eq(models.DeliveryStatusSent)).Count(ctx)) != 40 {
		t.Fatal("claims lost deliveries")
	}
}

func TestSQLiteMixedWorkload(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := db.InitializeFreeMonitors(ctx); err != nil {
		t.Fatal(err)
	}
	inputs := make([]models.FreeSubscriptionInput, 1000)
	for i := range inputs {
		id := fmt.Sprint(i)
		inputs[i] = models.FreeSubscriptionInput{
			Scope:           nook.Set("dm:" + id),
			Source:          nook.Set("gog"),
			DestinationKind: nook.Set(models.FreeSubscriptionDestinationKindDm),
			DestinationID:   nook.Set(id),
			ManagedBy:       nook.Set(id),
		}
	}
	if _, err := db.Client.FreeSubscription.CreateBulk(inputs...).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		add(t, db, fmt.Sprint(i), fmt.Sprint(i), testutil.Price(int64(i+1), 1000, time.Now()), false, nil)
	}
	apps := make([]domain.App, 3000)
	for i := range apps {
		apps[i] = domain.App{
			ID:   int64(i + 1),
			Name: fmt.Sprintf("Game %d", i),
			Type: "game",
		}
	}
	var group sync.WaitGroup
	group.Go(func() {
		if err := db.UpsertCatalog(ctx, apps); err != nil {
			t.Error(err)
		}
	})
	group.Go(func() {
		if err := db.RecordFreeScan(ctx, "gog", freegames.Result{
			Offers: []freegames.Offer{freeOffer()},
		}, time.Now()); err != nil {
			t.Error(err)
		}
	})
	group.Go(func() {
		for range 10 {
			item, err := db.Claim(ctx)
			if err != nil || item == nil {
				t.Errorf("mixed workload claim: %v, %v", item, err)
				return
			}

			if err := db.Finish(ctx, item.ID, models.DeliveryStatusSent, time.Now(), "test", ""); err != nil {
				t.Error(err)
				return
			}
		}
	})
	latencies := make([]time.Duration, 50)
	for i := range latencies {
		started := time.Now()
		if _, err := db.Search(ctx, "game"); err != nil {
			t.Error(err)
			break
		}
		latencies[i] = time.Since(started)
	}
	group.Wait()
	slices.Sort(latencies)
	t.Logf("concurrent catalog import, 1,000-subscriber scan and delivery claims: read p95=%s max=%s", latencies[47], latencies[49])
	if testutil.Must(db.Client.FreeDelivery.Query().Count(ctx)) != 1000 || testutil.Must(db.Client.Delivery.Query().Where(models.DeliveryColumns.Status.Eq(models.DeliveryStatusSent)).Count(ctx)) != 10 {
		t.Fatal("mixed workload lost deliveries")
	}
}

func benchmarkDatabase(b *testing.B) *store.Store {
	b.Helper()
	path := filepath.Join(b.TempDir(), "benchmark.db")
	if err := database.Migrate(b.Context(), path, io.Discard); err != nil {
		b.Fatal(err)
	}
	db, err := store.Open(b.Context(), path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func BenchmarkCatalog(b *testing.B) {
	for _, bulk := range []bool{false, true} {
		b.Run(fmt.Sprintf("bulk=%t", bulk), func(b *testing.B) {
			db := benchmarkDatabase(b)
			apps := make([]domain.App, 1000)
			for i := range apps {
				apps[i] = domain.App{
					ID:   int64(i + 1),
					Name: fmt.Sprintf("Game %d", i),
					Type: "game",
				}
			}
			if err := db.UpsertCatalog(b.Context(), apps); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var err error
				if bulk {
					err = db.UpsertCatalog(b.Context(), apps)
				} else {
					err = db.Client.WithTx(b.Context(), func(tx *models.Client) error {
						for _, app := range apps {
							if _, err := tx.App.Create().SetID(app.ID).SetName(app.Name).SetNameFold(strings.ToLower(app.Name)).SetType(app.Type).SetLastModified(app.LastModified).SetPriceChangeNumber(app.PriceChangeNumber).SetUpdatedAt(time.Now()).OnConflict(models.AppColumns.ID).UpdateExcluded(models.AppColumns.Name, models.AppColumns.NameFold, models.AppColumns.Type, models.AppColumns.LastModified, models.AppColumns.PriceChangeNumber, models.AppColumns.UpdatedAt).Save(b.Context()); err != nil {
								return err
							}
						}

						return nil
					})
				}

				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFreeScanFanout(b *testing.B) {
	db := benchmarkDatabase(b)
	ctx := b.Context()
	if err := db.InitializeFreeMonitors(ctx); err != nil {
		b.Fatal(err)
	}
	inputs := make([]models.FreeSubscriptionInput, 1000)
	for i := range inputs {
		id := fmt.Sprint(i)
		inputs[i] = models.FreeSubscriptionInput{
			Scope:           nook.Set("dm:" + id),
			Source:          nook.Set("gog"),
			DestinationKind: nook.Set(models.FreeSubscriptionDestinationKindDm),
			DestinationID:   nook.Set(id),
			ManagedBy:       nook.Set(id),
		}
	}
	if _, err := db.Client.FreeSubscription.CreateBulk(inputs...).Exec(ctx); err != nil {
		b.Fatal(err)
	}
	offer := freeOffer()
	iteration := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		offer.Campaign = fmt.Sprint(iteration)
		if err := db.RecordFreeScan(ctx, "gog", freegames.Result{
			Offers: []freegames.Offer{offer},
		}, time.Now()); err != nil {
			b.Fatal(err)
		}
		iteration++
	}
}
