package commands

import (
	"context"
	"time"

	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/preconditions"
)

const cooldownCapacity = 4096

func (h *Handler) rateLimit(bucket string, limit int64, duration time.Duration) preconditions.Check {
	h.cooldownMu.Lock()
	defer h.cooldownMu.Unlock()
	if h.cooldowns == nil {
		h.cooldowns = make(map[string]*preconditions.Cooldown)
	}
	cooldown := h.cooldowns[bucket]
	var err error
	if cooldown == nil {
		cooldown, err = preconditions.NewCooldown(preconditions.CooldownConfig{
			Limit:   int(limit),
			Window:  duration,
			MaxKeys: cooldownCapacity,
			Key: func(_ context.Context, i *api.Interaction) (string, error) {
				return owner(i)
			},
		})
		if err == nil {
			h.cooldowns[bucket] = cooldown
		}
	}

	return preconditions.Func(func(ctx context.Context, i *api.Interaction) error {
		failure := err
		if failure == nil {
			failure = cooldown.Check(ctx, i)
		}

		if failure != nil {
			h.Diagnostics.Observe("limit", bucket, 0, failure)
		}

		return failure
	})
}
