package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/gateway"
	"github.com/depthbomb/tomogo/schedule"
)

func presenceJob(count func(context.Context) (int, error), publish func(context.Context, gateway.Presence) error) schedule.Job {
	job := periodicJob("presence", time.Minute, 15*time.Second, func(ctx context.Context) error {
		games, err := count(ctx)
		if err != nil {
			return fmt.Errorf("count tracked games: %w", err)
		}
		noun := "games"
		if games == 1 {
			noun = "game"
		}
		status := fmt.Sprintf("Tracking %d %s", games, noun)

		return publish(ctx, gateway.Presence{
			Status: api.PresenceStatusOnline,
			Activities: []gateway.Activity{{
				Name:  "Custom Status",
				Type:  api.ActivityCustom,
				State: &status,
			}},
		})
	})
	job.WaitUntilReady = true

	return job
}

func publishPresence(ctx context.Context, manager *gateway.Manager, presence gateway.Presence) error {
	var failures []error
	for _, id := range manager.Snapshot().ShardIDs {
		session, ok := manager.Session(id)
		if !ok {
			continue
		}

		if err := session.UpdatePresence(ctx, presence); err != nil {
			failures = append(failures, fmt.Errorf("update shard %d presence: %w", id, err))
		}
	}

	return errors.Join(failures...)
}
