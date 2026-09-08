package store

import (
	"context"
	"time"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/freedelivery"
	"github.com/depthbomb/dealfox/ent/freeoffer"
	"github.com/depthbomb/dealfox/ent/freesubscription"
)

// Keep deduplication rows as long as their offer remains observable.
func (s *Store) PurgeFree(ctx context.Context, retention time.Duration) error {
	return s.write(ctx, func(c *ent.Client) error {
		offers, err := c.FreeOffer.Query().Where(freeoffer.LastSeenAtLT(time.Now().Add(-retention))).Limit(500).All(ctx)
		if err != nil {
			return err
		}
		for _, offer := range offers {
			if _, err := c.FreeDelivery.Delete().Where(freedelivery.OfferIDEQ(offer.ID)).Exec(ctx); err != nil {
				return err
			}

			if err := c.FreeOffer.DeleteOne(offer).Exec(ctx); err != nil {
				return err
			}
		}
		subs, err := c.FreeSubscription.Query().Where(freesubscription.Enabled(false), freesubscription.UpdatedAtLT(time.Now().Add(-retention))).Limit(500).All(ctx)
		if err != nil {
			return err
		}
		for _, sub := range subs {
			exists, err := c.FreeDelivery.Query().Where(freedelivery.SubscriptionIDEQ(sub.ID)).Exist(ctx)
			if err != nil {
				return err
			}

			if !exists {
				if err := c.FreeSubscription.DeleteOne(sub).Exec(ctx); err != nil {
					return err
				}
			}
		}

		return nil
	})
}
