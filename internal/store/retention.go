package store

import (
	"context"
	"time"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/delivery"
	"github.com/depthbomb/dealfox/ent/event"
	"github.com/depthbomb/dealfox/ent/observation"
	"github.com/depthbomb/dealfox/ent/rule"
)

func (s *Store) Purge(ctx context.Context, observationAge, deliveryAge, ruleAge time.Duration) (int, error) {
	var count int
	err := s.write(ctx, func(c *ent.Client) error {
		ids, err := c.Delivery.Query().Where(delivery.StatusIn(delivery.StatusSent, delivery.StatusDead, delivery.StatusCancelled), delivery.UpdatedAtLT(time.Now().Add(-deliveryAge))).Limit(1000).IDs(ctx)
		if err != nil {
			return err
		}

		n, err := c.Delivery.Delete().Where(delivery.IDIn(ids...)).Exec(ctx)
		if err != nil {
			return err
		}
		count += n

		ids, err = c.Event.Query().Where(event.Not(event.HasDeliveries()), event.CreatedAtLT(time.Now().Add(-deliveryAge))).Limit(1000).IDs(ctx)
		if err != nil {
			return err
		}

		n, err = c.Event.Delete().Where(event.IDIn(ids...)).Exec(ctx)
		if err != nil {
			return err
		}
		count += n

		ids, err = c.Observation.Query().Where(observation.Not(observation.HasEvents()), observation.ObservedAtLT(time.Now().Add(-observationAge))).Limit(1000).IDs(ctx)
		if err != nil {
			return err
		}

		n, err = c.Observation.Delete().Where(observation.IDIn(ids...)).Exec(ctx)
		if err != nil {
			return err
		}
		count += n

		ids, err = c.Rule.Query().Where(rule.Enabled(false), rule.Not(rule.HasEvents()), rule.UpdatedAtLT(time.Now().Add(-ruleAge))).Limit(1000).IDs(ctx)
		if err != nil {
			return err
		}

		n, err = c.Rule.Delete().Where(rule.IDIn(ids...)).Exec(ctx)
		count += n

		return err
	})

	return count, err
}
