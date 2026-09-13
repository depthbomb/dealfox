package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/nook"
)

func queueFree(ctx context.Context, c *models.Client, o *models.FreeOffer, sub *models.FreeSubscription) error {
	return discard(c.FreeDelivery.Create().SetOfferID(o.ID).SetOfferGeneration(o.Generation).SetSubscriptionID(sub.ID).SetDestinationKind(models.FreeDeliveryDestinationKind(sub.DestinationKind)).SetDestinationID(sub.DestinationID).SetPayload(o.Payload).OnConflict(models.FreeDeliveryColumns.OfferID, models.FreeDeliveryColumns.OfferGeneration, models.FreeDeliveryColumns.SubscriptionID).DoNothing().Save(ctx))
}

func (s *Store) FreeSubscriptions(ctx context.Context, scope string) ([]*models.FreeSubscription, error) {
	return s.Client.FreeSubscription.Query().Where(models.FreeSubscriptionColumns.Scope.Eq(scope), models.FreeSubscriptionColumns.Enabled.Eq(true)).OrderBy(models.FreeSubscriptionColumns.Source.Asc()).All(ctx)
}

// SetFreeSubscriptions adds selected sources. A server has one channel across its sources.
func (s *Store) SetFreeSubscriptions(ctx context.Context, scope, destination, manager string, sources []string, enabled bool) error {
	if !strings.HasPrefix(scope, "dm:") && !strings.HasPrefix(scope, "guild:") {
		return errors.New("invalid subscription scope")
	}

	validated, err := freegames.ParseSources(strings.Join(sources, ","))
	if err != nil {
		return err
	}

	return s.write(ctx, func(c *models.Client) error {
		kind := models.FreeSubscriptionDestinationKindDm
		if strings.HasPrefix(scope, "guild:") {
			kind = models.FreeSubscriptionDestinationKindChannel
		}

		if enabled && kind == models.FreeSubscriptionDestinationKindChannel {
			subs, err := c.FreeSubscription.Query().Where(models.FreeSubscriptionColumns.Scope.Eq(scope)).All(ctx)
			if err != nil {
				return err
			}
			for _, sub := range subs {
				if _, err := c.FreeDelivery.Update().Where(models.FreeDeliveryColumns.SubscriptionID.Eq(sub.ID), models.FreeDeliveryColumns.Status.In(models.FreeDeliveryStatusPending, models.FreeDeliveryStatusRetry)).SetDestinationID(destination).Exec(ctx); err != nil {
					return err
				}
			}

			if _, err := c.FreeSubscription.Update().Where(models.FreeSubscriptionColumns.Scope.Eq(scope)).SetDestinationID(destination).SetManagedBy(manager).Exec(ctx); err != nil {
				return err
			}
		}

		for _, source := range validated {
			if !enabled {
				sub, err := c.FreeSubscription.Query().Where(models.FreeSubscriptionColumns.Scope.Eq(scope), models.FreeSubscriptionColumns.Source.Eq(source)).Only(ctx)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}

				if err != nil {
					return err
				}

				if _, err := c.FreeSubscription.UpdateOneID(sub.ID).SetEnabled(false).SetManagedBy(manager).Exec(ctx); err != nil {
					return err
				}

				if _, err := c.FreeDelivery.Update().Where(models.FreeDeliveryColumns.SubscriptionID.Eq(sub.ID), models.FreeDeliveryColumns.Status.In(models.FreeDeliveryStatusPending, models.FreeDeliveryStatusRetry, models.FreeDeliveryStatusDead)).SetStatus(models.FreeDeliveryStatusCancelled).Exec(ctx); err != nil {
					return err
				}
				continue
			}

			if _, err := c.FreeSubscription.Create().SetScope(scope).SetSource(source).SetDestinationKind(kind).SetDestinationID(destination).SetManagedBy(manager).OnConflict(models.FreeSubscriptionColumns.Scope, models.FreeSubscriptionColumns.Source).Update(models.FreeSubscriptionChanges{
				Enabled:       nook.Set(true),
				DestinationID: nook.Set(destination),
				ManagedBy:     nook.Set(manager),
				UpdatedAt:     nook.Set(time.Now()),
			}).Save(ctx); err != nil {
				return err
			}

			sub, err := c.FreeSubscription.Query().Where(models.FreeSubscriptionColumns.Scope.Eq(scope), models.FreeSubscriptionColumns.Source.Eq(source)).Only(ctx)
			if err != nil {
				return err
			}
			age := 2 * time.Hour
			if source == "ubisoft" {
				age = 8 * time.Hour
			}
			offers, err := c.FreeOffer.Query().Where(models.FreeOfferColumns.Source.Eq(source), models.FreeOfferColumns.Eligible.Eq(true), models.FreeOfferColumns.LastSeenAt.GT(time.Now().Add(-age))).All(ctx)
			if err != nil {
				return err
			}
			for _, offer := range offers {
				if offer.Payload.Data.Publishable(time.Now()) {
					if err := queueFree(ctx, c, offer, sub); err != nil {
						return err
					}
				}
			}
		}

		return nil
	})
}

func (s *Store) FreeMonitors(ctx context.Context) ([]*models.FreeMonitor, error) {
	return s.Client.FreeMonitor.Query().OrderBy(models.FreeMonitorColumns.ID.Asc()).All(ctx)
}

func (s *Store) InitializeFreeMonitors(ctx context.Context) error {
	for _, source := range freegames.Sources() {
		if _, err := s.Client.FreeMonitor.Create().SetID(source).OnConflict(models.FreeMonitorColumns.ID).DoNothing().Save(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) RecordFreeScan(ctx context.Context, source string, result freegames.Result, next time.Time) error {
	return s.write(ctx, func(c *models.Client) error {
		subs, err := c.FreeSubscription.Query().Where(models.FreeSubscriptionColumns.Source.Eq(source), models.FreeSubscriptionColumns.Enabled.Eq(true)).All(ctx)
		if err != nil {
			return err
		}

		seen := make([]string, 0, len(result.Offers))
		for _, payload := range result.Offers {
			if payload.Source != source || payload.ProductID == "" || payload.Campaign == "" {
				continue
			}

			eligible := payload.Publishable(time.Now())
			identity := payload.Key()
			seen = append(seen, identity)
			previous, err := c.FreeOffer.Query().Where(models.FreeOfferColumns.Identity.Eq(identity)).Only(ctx)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}

			changes := models.FreeOfferChanges{
				Payload:    nook.Set(nook.JSON[freegames.Offer]{Data: payload}),
				Eligible:   nook.Set(eligible),
				LastSeenAt: nook.Set(time.Now()),
			}
			if eligible {
				changes.Closed = nook.Set(false)
				if previous != nil && previous.Closed {
					changes.Generation = nook.Set(previous.Generation + 1)
				}
			}

			offer, err := c.FreeOffer.Create().SetIdentity(identity).SetSource(source).SetPayload(nook.JSON[freegames.Offer]{Data: payload}).SetEligible(eligible).OnConflict(models.FreeOfferColumns.Identity).Update(changes).Save(ctx)
			if err != nil {
				return err
			}

			if !eligible {
				continue
			}

			for start := 0; start < len(subs); start += 500 {
				batch := subs[start:min(start+500, len(subs))]
				inputs := make([]models.FreeDeliveryInput, 0, len(batch))
				for _, sub := range batch {
					inputs = append(inputs, models.FreeDeliveryInput{
						OfferID:         nook.Set(offer.ID),
						OfferGeneration: nook.Set(offer.Generation),
						SubscriptionID:  nook.Set(sub.ID),
						DestinationKind: nook.Set(models.FreeDeliveryDestinationKind(sub.DestinationKind)),
						DestinationID:   nook.Set(sub.DestinationID),
						Payload:         nook.Set(offer.Payload),
					})
				}

				if _, err := c.FreeDelivery.CreateBulk(inputs...).OnConflict(models.FreeDeliveryColumns.OfferID, models.FreeDeliveryColumns.OfferGeneration, models.FreeDeliveryColumns.SubscriptionID).DoNothing().Exec(ctx); err != nil {
					return err
				}
			}
		}

		status := "ok"
		update := c.FreeMonitor.UpdateOneID(source).SetCheckedAt(time.Now()).SetNextCheckAt(next).SetProblems(nook.JSON[[]string]{Data: result.Problems})
		if len(result.Problems) > 0 {
			status = "degraded"
			update.AddFailures(1)
		} else {
			update.SetFailures(0)
			// An unavailable branch never invalidates the last successful observation.
			if _, err := c.FreeOffer.Update().Where(models.FreeOfferColumns.Source.Eq(source), models.FreeOfferColumns.Identity.NotIn(seen...)).SetEligible(false).SetClosed(true).Exec(ctx); err != nil {
				return err
			}
		}

		return discard(update.SetStatus(status).Exec(ctx))
	})
}

func (s *Store) RecoverFree(ctx context.Context, startup bool) error {
	q := s.Client.FreeDelivery.Update().Where(models.FreeDeliveryColumns.Status.Eq(models.FreeDeliveryStatusSending))
	if !startup {
		q.Where(models.FreeDeliveryColumns.UpdatedAt.LT(time.Now().Add(-5 * time.Minute)))
	}

	return discard(q.SetStatus(models.FreeDeliveryStatusRetry).SetNextAttemptAt(time.Now()).Exec(ctx))
}

func (s *Store) ClaimFree(ctx context.Context) (*models.FreeDelivery, error) {
	var result *models.FreeDelivery
	err := s.write(ctx, func(c *models.Client) error {
		d, err := c.FreeDelivery.Query().Where(models.FreeDeliveryColumns.Status.In(models.FreeDeliveryStatusPending, models.FreeDeliveryStatusRetry), models.FreeDeliveryColumns.NextAttemptAt.LTE(time.Now())).OrderBy(models.FreeDeliveryColumns.NextAttemptAt.Asc()).First(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		if err != nil {
			return err
		}

		sub, err := c.FreeSubscription.Get(ctx, d.SubscriptionID)
		if err != nil {
			return err
		}
		offer, err := c.FreeOffer.Get(ctx, d.OfferID)
		if err != nil {
			return err
		}

		maxAge := 2 * time.Hour
		if d.Payload.Data.Source == "ubisoft" {
			maxAge = 8 * time.Hour
		}

		if !sub.Enabled || !offer.Eligible || offer.Generation != d.OfferGeneration || !offer.Payload.Data.Publishable(time.Now()) {
			return discard(c.FreeDelivery.UpdateOneID(d.ID).SetStatus(models.FreeDeliveryStatusCancelled).SetLastError("subscription disabled or offer no longer eligible").Exec(ctx))
		}

		if time.Since(offer.LastSeenAt) > maxAge {
			if time.Since(d.CreatedAt) > 72*time.Hour {
				return discard(c.FreeDelivery.UpdateOneID(d.ID).SetStatus(models.FreeDeliveryStatusCancelled).SetLastError("fresh confirmation unavailable within 72 hours").Exec(ctx))
			}

			return discard(c.FreeDelivery.UpdateOneID(d.ID).SetStatus(models.FreeDeliveryStatusRetry).SetNextAttemptAt(time.Now().Add(time.Hour)).SetLastError("waiting for fresh offer confirmation").Exec(ctx))
		}

		result, err = c.FreeDelivery.UpdateOneID(d.ID).Where(models.FreeDeliveryColumns.Status.Eq(d.Status)).SetStatus(models.FreeDeliveryStatusSending).SetDestinationID(sub.DestinationID).SetPayload(offer.Payload).AddAttemptCount(1).Save(ctx)

		return err
	})

	return result, err
}

func (s *Store) FinishFree(ctx context.Context, id string, status models.FreeDeliveryStatus, retryAt time.Time, message, reason string) error {
	return discard(s.Client.FreeDelivery.Update().Where(models.FreeDeliveryColumns.ID.Eq(id), models.FreeDeliveryColumns.Status.Eq(models.FreeDeliveryStatusSending)).SetStatus(status).SetNextAttemptAt(retryAt).SetMessageID(message).SetLastError(reason).Exec(ctx))
}

func (s *Store) AdministerFree(ctx context.Context, id string, retry bool) error {
	q := s.Client.FreeDelivery.Update().Where(models.FreeDeliveryColumns.ID.Eq(id))
	if retry {
		q.Where(models.FreeDeliveryColumns.Status.Eq(models.FreeDeliveryStatusDead)).SetStatus(models.FreeDeliveryStatusRetry).SetNextAttemptAt(time.Now()).SetLastError("")
	} else {
		q.Where(models.FreeDeliveryColumns.Status.In(models.FreeDeliveryStatusPending, models.FreeDeliveryStatusRetry, models.FreeDeliveryStatusDead)).SetStatus(models.FreeDeliveryStatusCancelled)
	}

	n, err := q.Exec(ctx)
	if err == nil && n == 0 {
		return errors.New("no free-game delivery in an eligible state was found")
	}

	return err
}
