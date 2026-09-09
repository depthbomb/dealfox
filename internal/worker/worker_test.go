package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/delivery"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/rest"
)

type steamFake struct {
	batches   [][]int64
	countries []string
}

type senderFake struct {
	err   error
	calls int
}

func (s *steamFake) Details(context.Context, int64, string) (domain.Price, error) {
	return domain.Price{}, nil
}

func (s *steamFake) Catalog(context.Context, func(context.Context, []domain.App) error) error {
	return nil
}

func (s *steamFake) Prices(_ context.Context, ids []int64, country string) (map[int64]domain.Price, error) {
	s.batches = append(s.batches, append([]int64(nil), ids...))
	s.countries = append(s.countries, country)
	if len(ids) > 1 {
		return nil, errors.New("batch rejected")
	}

	p := testutil.Price(ids[0], 1000, time.Now())
	p.Country = country

	return map[int64]domain.Price{
		ids[0]: p,
	}, nil
}

func (s *senderFake) Send(context.Context, string, string, domain.Payload) (string, error) {
	s.calls++

	return "456", s.err
}

func TestPollingAndDelivery(t *testing.T) {
	db, _ := testutil.Database(t)
	ctx := t.Context()
	cfg := testutil.Config(t)
	steam := &steamFake{}
	sender := &senderFake{}
	w := &Worker{
		Tracker: &tracker.Service{
			Store:  db,
			Steam:  steam,
			Config: cfg,
		},
		Sender: sender,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for index, id := range []int64{10, 10, 20} {
		_, err := db.Add(ctx, store.AddRequest{
			OwnerID:   string(rune('a' + index)),
			Price:     testutil.Price(id, 2000, time.Now().Add(-2*time.Hour)),
			Condition: domain.AnySale,
			Maximum:   25,
			Interval:  time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Poll(ctx); err != nil {
		t.Fatal(err)
	}

	if len(steam.batches) != 3 || len(steam.batches[0]) != 2 || db.Client.Delivery.Query().CountX(ctx) != 3 {
		t.Fatalf("poll did not group subscribers and split failed batch: %v", steam.batches)
	}

	sender.err = &rest.DMError{
		Stage: rest.DMStageSend,
		Err: &rest.Error{
			StatusCode: 403,
		},
	}
	if err := w.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}

	dead := db.Client.Delivery.Query().Where(delivery.StatusEQ(delivery.StatusDead)).OnlyX(ctx)
	if dead.AttemptCount != 1 {
		t.Fatal("permanent error did not dead-letter immediately")
	}

	sender.err = errors.New("connection reset")
	if err := w.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}

	retry := db.Client.Delivery.Query().Where(delivery.StatusEQ(delivery.StatusRetry)).OnlyX(ctx)
	if !retry.NextAttemptAt.After(time.Now()) {
		t.Fatal("transient error was not delayed")
	}

	sender.err = nil
	if err := w.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}

	if db.Client.Delivery.Query().Where(delivery.StatusEQ(delivery.StatusSent)).CountX(ctx) != 1 {
		t.Fatal("successful send was not recorded")
	}

	if err := db.Administer(ctx, dead.ID, true); err != nil {
		t.Fatal(err)
	}

	if err := w.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}

	if db.Client.Delivery.GetX(ctx, dead.ID).Status != delivery.StatusSent {
		t.Fatal("operator retry did not send")
	}

	cfg.DeliveryEnabled = false
	before := sender.calls
	if err := w.Dispatch(ctx); err != nil || sender.calls != before {
		t.Fatal("delivery kill switch was ignored")
	}
}

func TestRetryPolicy(t *testing.T) {
	cfg := testutil.Config(t)
	now := time.Now()
	d := &ent.Delivery{
		ID:           "abcdefghijklmnopqrstuvwx",
		CreatedAt:    now,
		AttemptCount: 1,
	}
	when, ok := RetryAt(cfg, d, now)
	if !ok || when.Sub(now) < 4*time.Second || when.Sub(now) > 6*time.Second {
		t.Fatalf("initial jitter outside bounds: %v", when.Sub(now))
	}

	d.AttemptCount = 1000000
	when, ok = RetryAt(cfg, d, now)
	if !ok || when.Sub(now) > cfg.DeliveryRetryMaximumDelay {
		t.Fatal("retry backoff overflowed cap")
	}

	expires := d.CreatedAt.Add(cfg.DeliveryRetryMaximumAge)
	when, ok = RetryAt(cfg, d, expires.Add(-time.Second))
	if !ok || when.After(expires) {
		t.Fatal("retry exceeded maximum age")
	}

	if _, ok := RetryAt(cfg, d, expires); ok {
		t.Fatal("expired failure was not dead-lettered")
	}
}
