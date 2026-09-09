package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/depthbomb/tomogo"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/gateway"
	"github.com/depthbomb/tomogo/schedule"
)

func TestPresenceRefreshWaitsForReadyRepeatsAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ready := make(chan struct{})
		updates := make(chan gateway.Presence, 4)
		var games atomic.Int64
		job := presenceJob(func(ctx context.Context) (int, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("count query has no deadline")
			}

			return int(games.Load()), ctx.Err()
		}, func(_ context.Context, presence gateway.Presence) error {
			updates <- presence

			return nil
		})
		app, err := tomogo.New(tomogo.Config{
			Delivery: tomogo.DeliveryHTTP,
			Schedules: schedule.Config{
				WaitReady: func(ctx context.Context) error {
					select {
					case <-ready:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := registerJobs(app.Schedules(), []schedule.Job{job})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- runApplication(ctx, app, tasks...)
		}()
		synctest.Wait()
		if len(updates) != 0 {
			t.Fatal("published before Gateway readiness")
		}
		close(ready)
		synctest.Wait()
		for i, want := range []string{"Tracking 0 games", "Tracking 1 game", "Tracking 12 games"} {
			if i > 0 {
				games.Store([]int64{0, 1, 12}[i])
				time.Sleep(59 * time.Second)
				synctest.Wait()
				if len(updates) != 0 {
					t.Fatal("published before one minute elapsed")
				}
				time.Sleep(time.Second)
				synctest.Wait()
			}
			if len(updates) != 1 {
				t.Fatalf("updates = %d, want 1", len(updates))
			}
			presence := <-updates
			if err := presence.Validate(); err != nil {
				t.Fatal(err)
			}

			if presence.Status != api.PresenceStatusOnline || len(presence.Activities) != 1 || presence.Activities[0].Type != api.ActivityCustom || presence.Activities[0].State == nil || *presence.Activities[0].State != want {
				t.Fatalf("unexpected presence: %+v", presence)
			}
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if len(updates) != 0 {
			t.Fatal("published after shutdown")
		}
	})
}

func TestPresenceFailureDoesNotPublishFalseCount(t *testing.T) {
	failure := errors.New("database unavailable")
	job := presenceJob(func(context.Context) (int, error) {
		return 0, failure
	}, func(context.Context, gateway.Presence) error {
		t.Fatal("published a count after query failure")

		return nil
	})
	if err := job.Handler(t.Context(), schedule.Run{}); !errors.Is(err, failure) {
		t.Fatalf("query failure = %v", err)
	}
	job = presenceJob(func(context.Context) (int, error) {
		return 5, nil
	}, func(ctx context.Context, _ gateway.Presence) error {
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := job.Handler(ctx, schedule.Run{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("publish cancellation = %v", err)
	}
}
