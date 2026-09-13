package testutil

import (
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/database"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store"
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

// Database creates a migrated SQLite file owned and removed by the test.
func Database(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dealfox.db")
	if err := database.Migrate(t.Context(), path, io.Discard); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.ValidateMigrations(t.Context()); err != nil {
		t.Fatal(err)
	}

	return db, path
}

// Must preserves the panic-on-error behavior of the previous generated test helpers.
func Must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}

	return value
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
