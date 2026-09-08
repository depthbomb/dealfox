package commands

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/preconditions"
)

type window struct {
	count int64
	until time.Time
}

type limiter struct {
	mu        sync.Mutex
	windows   map[string]window
	nextSweep time.Time
}

func (l *limiter) allow(key string, limit int64, duration time.Duration, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.windows == nil {
		l.windows = make(map[string]window)
	}

	if !now.Before(l.nextSweep) {
		for key, w := range l.windows {
			if !now.Before(w.until) {
				delete(l.windows, key)
			}
		}

		l.nextSweep = now.Add(time.Minute)
	}

	w := l.windows[key]
	if !now.Before(w.until) {
		w = window{
			until: now.Add(duration),
		}
	}

	if w.count >= limit {
		seconds := (w.until.Sub(now)-1)/time.Second + 1
		unit := "seconds"
		if seconds == 1 {
			unit = "second"
		}

		return &preconditions.Failure{
			Reason: fmt.Sprintf("Please try again in %d %s.", seconds, unit),
		}
	}

	w.count++
	l.windows[key] = w

	return nil
}

func (h *Handler) rateLimit(bucket string, limit int64, duration time.Duration) preconditions.Check {
	return preconditions.Func(func(_ context.Context, i *api.Interaction) error {
		user, err := owner(i)
		if err != nil {
			return err
		}

		err = h.limiter.allow(bucket+":"+user, limit, duration, time.Now())
		if err != nil {
			h.Diagnostics.Observe("limit", bucket, 0, err)
		}

		return err
	})
}
