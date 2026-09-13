package store

import (
	"context"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/nook"
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

	return s.write(ctx, func(c *models.Client) error {
		ownedRules := models.RuleColumns.OwnerID.Eq(owner)
		ownedEvents := models.EventColumns.HasRuleWith(ownedRules)
		if _, err := c.Delivery.Delete().Where(nook.Or(models.DeliveryColumns.DestinationID.Eq(owner), models.DeliveryColumns.HasEventWith(ownedEvents))).Exec(ctx); err != nil {
			return err
		}

		if _, err := c.Event.Delete().Where(ownedEvents).Exec(ctx); err != nil {
			return err
		}

		if _, err := c.Rule.Delete().Where(ownedRules).Exec(ctx); err != nil {
			return err
		}

		personal := nook.Or(models.FreeSubscriptionColumns.Scope.Eq("dm:"+owner), nook.And(models.FreeSubscriptionColumns.DestinationKind.Eq(models.FreeSubscriptionDestinationKindDm), models.FreeSubscriptionColumns.DestinationID.Eq(owner)))
		ids, err := c.FreeSubscription.Query().Where(personal).IDs(ctx)
		if err != nil {
			return err
		}

		if _, err := c.FreeDelivery.Delete().Where(nook.Or(models.FreeDeliveryColumns.SubscriptionID.In(ids...), nook.And(models.FreeDeliveryColumns.DestinationKind.Eq(models.FreeDeliveryDestinationKindDm), models.FreeDeliveryColumns.DestinationID.Eq(owner)))).Exec(ctx); err != nil {
			return err
		}

		if _, err := c.FreeSubscription.Delete().Where(personal).Exec(ctx); err != nil {
			return err
		}

		return discard(c.FreeSubscription.Update().Where(models.FreeSubscriptionColumns.ManagedBy.Eq(owner)).SetManagedBy("deleted").Exec(ctx))
	})
}
