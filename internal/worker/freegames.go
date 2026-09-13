package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/diagnostics"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/store/models"
)

type FreeScanner interface {
	Scan(context.Context, string) freegames.Result
}

type FreeSender interface {
	SendFree(context.Context, string, string, string, freegames.Offer) (string, error)
}

type FreeGames struct {
	Store       *store.Store
	Scanner     FreeScanner
	Sender      FreeSender
	Config      *config.Config
	Logger      *slog.Logger
	Diagnostics *diagnostics.Recorder
	pollMu      sync.Mutex
	dispatchMu  sync.Mutex
}

func (w *FreeGames) Poll(ctx context.Context) error {
	if !w.Config.FreeGamesEnabled {
		return nil
	}

	w.pollMu.Lock()
	defer w.pollMu.Unlock()
	monitors, err := w.Store.FreeMonitors(ctx)
	if err != nil {
		return err
	}
	for _, monitor := range monitors {
		if time.Now().Before(monitor.NextCheckAt) {
			continue
		}

		pollCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		started := time.Now()
		result := w.Scanner.Scan(pollCtx, monitor.ID)
		var scanErr error
		if len(result.Problems) > 0 {
			scanErr = errors.New("source degraded")
		}
		w.Diagnostics.Observe("source", monitor.ID, time.Since(started), errors.Join(pollCtx.Err(), scanErr))
		w.Diagnostics.Sample("source", monitor.ID, map[string]float64{
			"offers":   float64(len(result.Offers)),
			"problems": float64(len(result.Problems)),
		})
		cancel()

		if ctx.Err() != nil {
			return ctx.Err()
		}

		interval := w.Config.FreeGamesPollInterval
		if monitor.ID == "ubisoft" {
			interval = w.Config.FreeGamesUbisoftInterval
		}

		if len(result.Problems) > 0 {
			interval = min(interval*time.Duration(1<<min(monitor.Failures+1, 4)), 24*time.Hour)
			w.Logger.Warn("free game source degraded", "source", monitor.ID, "problems", result.Problems)
		}

		next := time.Now().Add(interval + time.Duration(rand.Int64N(int64(interval/10))))

		if err := w.Store.RecordFreeScan(ctx, monitor.ID, result, next); err != nil {
			return err
		}

		w.Logger.Info("free game source checked", "source", monitor.ID, "candidates", len(result.Offers), "problems", len(result.Problems))
	}

	return nil
}

func (w *FreeGames) Dispatch(ctx context.Context) (err error) {
	if !w.Config.FreeGamesEnabled || !w.Config.DeliveryEnabled {
		return nil
	}

	w.dispatchMu.Lock()
	defer w.dispatchMu.Unlock()
	release, err := w.Store.BeginDelivery(ctx)
	if err != nil {
		return err
	}
	defer release()

	if err := w.Store.RecoverFree(ctx, false); err != nil {
		return err
	}
	d, err := w.Store.ClaimFree(ctx)
	if err != nil || d == nil {
		return err
	}
	outcome := "free-retry"
	var sendErr error
	defer func() {
		w.Diagnostics.Observe("delivery", outcome, time.Since(d.CreatedAt), errors.Join(sendErr, err))
	}()

	sendCtx, cancel := context.WithTimeout(ctx, time.Minute)
	message, sendErr := w.Sender.SendFree(sendCtx, string(d.DestinationKind), d.DestinationID, d.ID, d.Payload.Data)
	cancel()

	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer finishCancel()
	if sendErr == nil {
		outcome = "free-sent"
		return w.Store.FinishFree(finishCtx, d.ID, models.FreeDeliveryStatusSent, time.Now(), message, "")
	}

	retry, ok := RetryAt(w.Config, &models.Delivery{
		ID:           d.ID,
		CreatedAt:    d.CreatedAt,
		AttemptCount: d.AttemptCount,
	}, time.Now())

	status := models.FreeDeliveryStatusRetry

	if permanent(sendErr) || !ok {
		outcome = "free-dead"
		status = models.FreeDeliveryStatusDead
	}

	w.Logger.Warn("free game delivery failed", "delivery_id", d.ID, "attempt", d.AttemptCount, "error", sendErr)

	return w.Store.FinishFree(finishCtx, d.ID, status, retry, "", fmt.Sprintf("Discord delivery failed on attempt %d", d.AttemptCount))
}

func (w *FreeGames) Purge(ctx context.Context) error {
	return w.Store.PurgeFree(ctx, max(w.Config.DeliveryRetention, 90*24*time.Hour))
}
