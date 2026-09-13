package store

import (
	"context"
	"time"

	"github.com/depthbomb/dealfox/internal/store/models"
)

// Keep deduplication rows as long as their offer remains observable.
func (s *Store) PurgeFree(ctx context.Context, retention time.Duration) error {
	return s.write(ctx, func(c *models.Client) error {
		offers, err := c.FreeOffer.Query().Where(models.FreeOfferColumns.LastSeenAt.LT(time.Now().Add(-retention))).Limit(500).All(ctx)
		if err != nil {
			return err
		}
		for _, offer := range offers {
			if _, err := c.FreeDelivery.Delete().Where(models.FreeDeliveryColumns.OfferID.Eq(offer.ID)).Exec(ctx); err != nil {
				return err
			}

			if _, err := c.FreeOffer.DeleteOneID(offer.ID).Exec(ctx); err != nil {
				return err
			}
		}
		subs, err := c.FreeSubscription.Query().Where(models.FreeSubscriptionColumns.Enabled.Eq(false), models.FreeSubscriptionColumns.UpdatedAt.LT(time.Now().Add(-retention))).Limit(500).All(ctx)
		if err != nil {
			return err
		}
		for _, sub := range subs {
			exists, err := c.FreeDelivery.Query().Where(models.FreeDeliveryColumns.SubscriptionID.Eq(sub.ID)).Exists(ctx)
			if err != nil {
				return err
			}

			if !exists {
				if _, err := c.FreeSubscription.DeleteOneID(sub.ID).Exec(ctx); err != nil {
					return err
				}
			}
		}

		return nil
	})
}
