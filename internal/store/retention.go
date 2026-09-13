package store

import (
	"context"
	"time"

	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/nook"
)

func (s *Store) Purge(ctx context.Context, observationAge, deliveryAge, ruleAge time.Duration) (int64, error) {
	var count int64
	err := s.write(ctx, func(c *models.Client) error {
		ids, err := c.Delivery.Query().Where(models.DeliveryColumns.Status.In(models.DeliveryStatusSent, models.DeliveryStatusDead, models.DeliveryStatusCancelled), models.DeliveryColumns.UpdatedAt.LT(time.Now().Add(-deliveryAge))).Limit(1000).IDs(ctx)
		if err != nil {
			return err
		}

		n, err := c.Delivery.Delete().Where(models.DeliveryColumns.ID.In(ids...)).Exec(ctx)
		if err != nil {
			return err
		}
		count += n

		ids, err = c.Event.Query().Where(nook.Not(models.EventColumns.HasDeliveries()), models.EventColumns.CreatedAt.LT(time.Now().Add(-deliveryAge))).Limit(1000).IDs(ctx)
		if err != nil {
			return err
		}

		n, err = c.Event.Delete().Where(models.EventColumns.ID.In(ids...)).Exec(ctx)
		if err != nil {
			return err
		}
		count += n

		ids, err = c.Observation.Query().Where(nook.Not(models.ObservationColumns.HasEvents()), models.ObservationColumns.ObservedAt.LT(time.Now().Add(-observationAge))).Limit(1000).IDs(ctx)
		if err != nil {
			return err
		}

		n, err = c.Observation.Delete().Where(models.ObservationColumns.ID.In(ids...)).Exec(ctx)
		if err != nil {
			return err
		}
		count += n

		ids, err = c.Rule.Query().Where(models.RuleColumns.Enabled.Eq(false), nook.Not(models.RuleColumns.HasEvents()), models.RuleColumns.UpdatedAt.LT(time.Now().Add(-ruleAge))).Limit(1000).IDs(ctx)
		if err != nil {
			return err
		}

		n, err = c.Rule.Delete().Where(models.RuleColumns.ID.In(ids...)).Exec(ctx)
		count += n

		return err
	})

	return count, err
}
