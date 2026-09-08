package store

import (
	"context"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/delivery"
	"github.com/depthbomb/dealfox/ent/event"
	"github.com/depthbomb/dealfox/ent/freedelivery"
	"github.com/depthbomb/dealfox/ent/freesubscription"
	"github.com/depthbomb/dealfox/ent/rule"
	"github.com/depthbomb/dealfox/internal/domain"
)

const deliverySlots = 1024

// BeginDelivery protects a claim, send, and completion from account deletion.
// The single running bot shares this store between both delivery workers.
func (s *Store) BeginDelivery(ctx context.Context) (func(), error) {
	if err := s.deliveryGate.Acquire(ctx, 1); err != nil {
		return nil, err
	}

	return func() {
		s.deliveryGate.Release(1)
	}, nil
}

// DeleteAccount removes personal data atomically after in-flight sends finish.
// Shared game data and server alerts survive without the former manager's ID.
func (s *Store) DeleteAccount(ctx context.Context, owner string) error {
	if owner == "" {
		return domain.Invalid("A user is required to delete an account.")
	}

	if err := s.deliveryGate.Acquire(ctx, deliverySlots); err != nil {
		return err
	}
	defer s.deliveryGate.Release(deliverySlots)

	return s.write(ctx, func(c *ent.Client) error {
		ownedRules := rule.OwnerIDEQ(owner)
		ownedEvents := event.HasRuleWith(ownedRules)
		if _, err := c.Delivery.Delete().Where(delivery.Or(delivery.DestinationIDEQ(owner), delivery.HasEventWith(ownedEvents))).Exec(ctx); err != nil {
			return err
		}

		if _, err := c.Event.Delete().Where(ownedEvents).Exec(ctx); err != nil {
			return err
		}

		if _, err := c.Rule.Delete().Where(ownedRules).Exec(ctx); err != nil {
			return err
		}

		personal := freesubscription.Or(freesubscription.ScopeEQ("dm:"+owner), freesubscription.And(freesubscription.DestinationKindEQ(freesubscription.DestinationKindDm), freesubscription.DestinationIDEQ(owner)))
		ids, err := c.FreeSubscription.Query().Where(personal).IDs(ctx)
		if err != nil {
			return err
		}

		if _, err := c.FreeDelivery.Delete().Where(freedelivery.Or(freedelivery.SubscriptionIDIn(ids...), freedelivery.And(freedelivery.DestinationKindEQ(freedelivery.DestinationKindDm), freedelivery.DestinationIDEQ(owner)))).Exec(ctx); err != nil {
			return err
		}

		if _, err := c.FreeSubscription.Delete().Where(personal).Exec(ctx); err != nil {
			return err
		}

		return c.FreeSubscription.Update().Where(freesubscription.ManagedByEQ(owner)).SetManagedBy("deleted").Exec(ctx)
	})
}
