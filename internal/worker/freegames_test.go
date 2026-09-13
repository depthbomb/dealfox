package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/tomogo/rest"
)

type freeFake struct {
	calls  int
	sends  int
	err    error
	result freegames.Result
}

func (f *freeFake) Scan(_ context.Context, source string) freegames.Result {
	f.calls++
	if source == "gog" {
		return f.result
	}

	return freegames.Result{}
}

func (f *freeFake) SendFree(context.Context, string, string, string, freegames.Offer) (string, error) {
	f.sends++

	return "message", f.err
}

func TestFreeWorkerPollingRetryAndPermanentFailure(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	if err := db.InitializeFreeMonitors(ctx); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"123", "456"} {
		if err := db.SetFreeSubscriptions(ctx, "dm:"+user, user, user, []string{"gog"}, true); err != nil {
			t.Fatal(err)
		}
	}
	fake := &freeFake{
		result: freegames.Result{
			Offers: []freegames.Offer{{
				Source:      "gog",
				ProductID:   "test",
				Campaign:    "test",
				Country:     "US",
				Platform:    "PC",
				Title:       "Game",
				Description: "A game",
				URL:         "https://www.gog.com/game/test",
				ImageURL:    "https://images.gog-statics.com/test.jpg",
				EndsAt:      time.Now().Add(time.Hour),
				Eligible:    true,
				Evidence: []freegames.Evidence{{
					URL: "https://www.gog.com/game/test",
				}},
			}},
		},
	}
	w := &FreeGames{
		Store:   db,
		Scanner: fake,
		Sender:  fake,
		Config:  testutil.Config(t),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for range 2 {
		if err := w.Poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if fake.calls != 4 {
		t.Fatalf("source schedules ignored: %d", fake.calls)
	}

	fake.err = &rest.Error{
		StatusCode: 403,
	}
	if err := w.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if count := testutil.Must(db.Client.FreeDelivery.Query().Where(models.FreeDeliveryColumns.Status.Eq(models.FreeDeliveryStatusDead)).Count(ctx)); count != 1 {
		t.Fatal("permanent failure did not dead-letter")
	}

	fake.err = errors.New("connection reset")
	if err := w.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	d := testutil.Must(db.Client.FreeDelivery.Query().Where(models.FreeDeliveryColumns.Status.Eq(models.FreeDeliveryStatusRetry)).Only(ctx))
	if !d.NextAttemptAt.After(time.Now()) {
		t.Fatal("transient failure did not back off")
	}
	if _, err := db.Client.FreeDelivery.UpdateOneID(d.ID).SetNextAttemptAt(time.Now()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fake.err = nil
	if err := w.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if count := testutil.Must(db.Client.FreeDelivery.Query().Where(models.FreeDeliveryColumns.Status.Eq(models.FreeDeliveryStatusSent)).Count(ctx)); count != 1 {
		t.Fatal("retry did not complete")
	}
}
