package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/nook"
)

type blockedAccountSender struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (s *blockedAccountSender) send(ctx context.Context) (string, error) {
	s.calls.Add(1)
	close(s.started)
	select {
	case <-s.release:
		return "message", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *blockedAccountSender) Send(ctx context.Context, _, _ string, _ domain.Payload) (string, error) {
	return s.send(ctx)
}

func (s *blockedAccountSender) SendFree(ctx context.Context, _, _, _ string, _ freegames.Offer) (string, error) {
	return s.send(ctx)
}

func TestDeletionWaitsForBothNotificationWorkers(t *testing.T) {
	for _, kind := range []string{"sale", "free"} {
		t.Run(kind, func(t *testing.T) {
			db, _ := testutil.Database(t)
			cfg := testutil.Config(t)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			sender := &blockedAccountSender{
				started: make(chan struct{}),
				release: make(chan struct{}),
			}
			var dispatch func(context.Context) error
			if kind == "sale" {
				for id := int64(1); id <= 2; id++ {
					_, err := db.Add(t.Context(), store.AddRequest{
						OwnerID:   "123",
						Price:     testutil.Price(id, 1000, time.Now()),
						Condition: domain.AnySale,
						Maximum:   25,
						Interval:  time.Hour,
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				w := &Worker{
					Tracker: &tracker.Service{
						Store:  db,
						Config: cfg,
					},
					Sender: sender,
					Logger: logger,
				}
				dispatch = w.Dispatch
			} else {
				if err := db.SetFreeSubscriptions(t.Context(), "dm:123", "123", "123", []string{"gog"}, true); err != nil {
					t.Fatal(err)
				}
				sub := testutil.Must(db.Client.FreeSubscription.Query().Only(t.Context()))
				for _, product := range []string{"one", "two"} {
					payload := freegames.Offer{
						Source:      "gog",
						ProductID:   product,
						Campaign:    "test",
						Country:     "US",
						Platform:    "PC",
						Title:       "Game",
						Description: "A complete game",
						ImageURL:    "https://images.gog-statics.com/test.jpg",
						URL:         "https://www.gog.com/game/test",
						Eligible:    true,
						EndsAt:      time.Now().Add(time.Hour),
						Evidence: []freegames.Evidence{
							{
								URL: "https://www.gog.com/game/test",
							},
						},
					}
					offer := testutil.Must(db.Client.FreeOffer.Create().SetIdentity(product).SetSource("gog").SetPayload(nook.JSON[freegames.Offer]{Data: payload}).SetEligible(true).Save(t.Context()))
					testutil.Must(db.Client.FreeDelivery.Create().SetOfferID(offer.ID).SetSubscriptionID(sub.ID).SetDestinationKind("dm").SetDestinationID("123").SetPayload(nook.JSON[freegames.Offer]{Data: payload}).Save(t.Context()))
				}
				w := &FreeGames{
					Store:  db,
					Config: cfg,
					Sender: sender,
					Logger: logger,
				}
				dispatch = w.Dispatch
			}

			done := make(chan error, 1)
			go func() {
				done <- dispatch(t.Context())
			}()
			select {
			case <-sender.started:
			case err := <-done:
				t.Fatalf("worker returned before sending: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not start sending")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			err := db.DeleteAccount(ctx, "123")
			cancel()
			close(sender.release)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("account deletion passed the sending worker: %v", err)
			}

			if err := <-done; err != nil {
				t.Fatal(err)
			}

			if err := db.DeleteAccount(t.Context(), "123"); err != nil {
				t.Fatal(err)
			}

			if err := dispatch(t.Context()); err != nil {
				t.Fatal(err)
			}

			if sender.calls.Load() != 1 {
				t.Fatal("queued notification was sent after account deletion")
			}
		})
	}
}
