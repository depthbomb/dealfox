package worker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/internal/diagnostics"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/tracker"
)

type Worker struct {
	Tracker     *tracker.Service
	Sender      Sender
	Logger      *slog.Logger
	Diagnostics *diagnostics.Recorder
	dispatchMu  sync.Mutex
}

func (w *Worker) SyncCatalog(ctx context.Context) error {
	return w.Tracker.Steam.Catalog(ctx, func(ctx context.Context, apps []domain.App) error {
		err := w.Tracker.Store.UpsertCatalog(ctx, apps)
		if err == nil {
			w.Diagnostics.Add("catalog", len(apps))
		}

		return err
	})
}

func (w *Worker) Purge(ctx context.Context) error {
	cfg := w.Tracker.Config
	for range 100 {
		count, err := w.Tracker.Store.Purge(ctx, cfg.ObservationRetention, cfg.DeliveryRetention, cfg.SubscriptionRetention)
		w.Diagnostics.Add("retention", count)
		if err != nil || count == 0 {
			return err
		}
	}

	return nil
}

func (w *Worker) Poll(ctx context.Context) error {
	if !w.Tracker.Config.PollingEnabled {
		return nil
	}

	targets, err := w.Tracker.Store.Due(ctx, int(w.Tracker.Config.PollBatchSize))
	if err != nil || len(targets) == 0 {
		return err
	}

	return w.fetch(ctx, targets)
}

func (w *Worker) fetch(ctx context.Context, targets []*ent.Target) error {
	ids := make([]int64, len(targets))
	for i, t := range targets {
		ids[i] = t.AppID
	}

	prices, err := w.Tracker.Steam.Prices(ctx, ids, targets[0].Country)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if len(targets) > 1 {
			middle := len(targets) / 2
			left := w.fetch(ctx, targets[:middle])
			right := w.fetch(ctx, targets[middle:])
			if left != nil {
				return left
			}

			return right
		}

		return w.Tracker.Store.TargetFailed(ctx, targets[0].ID)
	}

	for _, t := range targets {
		p, ok := prices[t.AppID]
		if !ok {
			if err := w.Tracker.Store.TargetFailed(ctx, t.ID); err != nil {
				return err
			}
			continue
		}

		p.Name = t.Edges.App.Name
		p.Type = t.Edges.App.Type
		if err := w.Tracker.Store.Observe(ctx, t.ID, p, w.Tracker.Config.PollInterval); err != nil {
			return err
		}
		w.Diagnostics.Add("poll", 1)
	}

	return nil
}
