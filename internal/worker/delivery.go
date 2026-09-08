package worker

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/delivery"
	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/tomogo/rest"
)

type Sender interface {
	Send(context.Context, string, string, domain.Payload) (string, error)
}

func permanent(err error) bool {
	if remote, ok := errors.AsType[*rest.Error](err); ok {
		return remote.StatusCode == 400 || remote.StatusCode == 401 || remote.StatusCode == 403 || remote.StatusCode == 404
	}

	return false
}

func RetryAt(cfg *config.Config, d *ent.Delivery, now time.Time) (time.Time, bool) {
	expires := d.CreatedAt.Add(cfg.DeliveryRetryMaximumAge)
	if !now.Before(expires) {
		return time.Time{}, false
	}

	delay := cfg.DeliveryRetryInitialDelay
	for attempt := 1; attempt < d.AttemptCount && delay < cfg.DeliveryRetryMaximumDelay; attempt++ {
		if delay > cfg.DeliveryRetryMaximumDelay/2 {
			delay = cfg.DeliveryRetryMaximumDelay
		} else {
			delay *= 2
		}
	}

	hash := fnv.New64a()
	_, _ = hash.Write([]byte(d.ID + strconv.Itoa(d.AttemptCount)))
	delay = min(time.Duration(float64(delay)*(float64(80+hash.Sum64()%41)/100)), cfg.DeliveryRetryMaximumDelay)
	when := now.Add(delay)
	if when.After(expires) {
		when = expires
	}

	return when, true
}

func (w *Worker) Dispatch(ctx context.Context) (err error) {
	if !w.Tracker.Config.DeliveryEnabled {
		return nil
	}

	w.dispatchMu.Lock()
	defer w.dispatchMu.Unlock()
	release, err := w.Tracker.Store.BeginDelivery(ctx)
	if err != nil {
		return err
	}
	defer release()

	if err := w.Tracker.Store.Recover(ctx, false); err != nil {
		return err
	}

	d, err := w.Tracker.Store.Claim(ctx)
	if err != nil || d == nil {
		return err
	}
	outcome := "sale-retry"
	var sendErr error
	defer func() {
		w.Diagnostics.Observe("delivery", outcome, time.Since(d.CreatedAt), errors.Join(sendErr, err))
	}()

	sendCtx, cancel := context.WithTimeout(ctx, time.Minute)
	message, sendErr := w.Sender.Send(sendCtx, d.DestinationID, d.ID, d.Edges.Event.Payload)
	cancel()

	// Persist a result even when shutdown canceled the Discord request.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer finishCancel()
	if sendErr == nil {
		outcome = "sale-sent"
		return w.Tracker.Store.Finish(finishCtx, d.ID, delivery.StatusSent, time.Now(), message, "")
	}

	retry, ok := RetryAt(w.Tracker.Config, d, time.Now())
	status := delivery.StatusRetry
	reason := "Discord transport failed"
	if permanent(sendErr) || !ok {
		outcome = "sale-dead"
		status = delivery.StatusDead
		reason = "Discord delivery permanently failed or exhausted its retry window"
	}

	if remote, ok := errors.AsType[*rest.Error](sendErr); ok {
		reason = fmt.Sprintf("Discord HTTP %d (code %d)", remote.StatusCode, remote.Code)
	}

	w.Logger.Warn("Discord delivery failed", "delivery_id", d.ID, "attempt", d.AttemptCount, "error", sendErr)

	return w.Tracker.Store.Finish(finishCtx, d.ID, status, retry, "", reason)
}
