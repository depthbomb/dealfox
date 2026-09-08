package testutil

import (
	"context"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/database"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/jackc/pgx/v5"
)

func Config(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadFrom(func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}

	return &cfg
}

// Database creates and removes only a uniquely named database owned by the test.
func Database(t *testing.T) (*store.Store, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is unset; PostgreSQL integration test skipped")
	}

	u, err := url.Parse(dsn)
	if err != nil || !strings.HasPrefix(u.Path, "/dealfox_test_") {
		t.Fatal("TEST_DATABASE_URL must name a disposable dealfox_test_* database")
	}

	name := "dealfox_test_" + cuid.Generate()
	u.Path = "/" + name
	dsn = u.String()
	ctx := t.Context()
	if err := database.Create(ctx, dsn); err != nil {
		t.Fatal(err)
	}

	adminURL := *u
	adminURL.Path = "/postgres"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn, err := pgx.Connect(cleanupCtx, adminURL.String())
		if err != nil {
			t.Error("could not connect for test database cleanup")

			return
		}
		defer conn.Close(cleanupCtx)
		if _, err := conn.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
	})

	if err := database.Migrate(ctx, dsn, io.Discard); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.ValidateMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	return db, dsn
}

func Price(id int64, final int64, at time.Time) domain.Price {
	discount := 50
	state := "on_sale"
	if final == 2000 {
		discount = 0
		state = "not_on_sale"
	}

	return domain.Price{
		AppID:        id,
		Name:         "Test Game",
		Type:         "game",
		Country:      "US",
		Availability: "priced",
		SaleState:    state,
		Currency:     "USD",
		Regular:      new(int64(2000)),
		Final:        new(final),
		Discount:     new(discount),
		ObservedAt:   at,
		SourceStatus: "ok",
	}
}
