package store

import (
	"context"
	"errors"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/freedelivery"
	"github.com/depthbomb/dealfox/ent/freemonitor"
	"github.com/depthbomb/dealfox/ent/freeoffer"
	"github.com/depthbomb/dealfox/ent/freesubscription"
	"github.com/depthbomb/dealfox/internal/freegames"
)

func queueFree(ctx context.Context, c *ent.Client, o *ent.FreeOffer, sub *ent.FreeSubscription) error {
	return c.FreeDelivery.Create().SetOfferID(o.ID).SetOfferGeneration(o.Generation).SetSubscriptionID(sub.ID).SetDestinationKind(freedelivery.DestinationKind(sub.DestinationKind)).SetDestinationID(sub.DestinationID).SetPayload(o.Payload).OnConflictColumns(freedelivery.FieldOfferID, freedelivery.FieldOfferGeneration, freedelivery.FieldSubscriptionID).Ignore().Exec(ctx)
}

func (s *Store) FreeSubscriptions(ctx context.Context, scope string) ([]*ent.FreeSubscription, error) {
	return s.Client.FreeSubscription.Query().Where(freesubscription.ScopeEQ(scope), freesubscription.Enabled(true)).Order(ent.Asc(freesubscription.FieldSource)).All(ctx)
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

	return s.write(ctx, func(c *ent.Client) error {
		kind := freesubscription.DestinationKindDm
		if strings.HasPrefix(scope, "guild:") {
			kind = freesubscription.DestinationKindChannel
		}

		if enabled && kind == freesubscription.DestinationKindChannel {
			subs, err := c.FreeSubscription.Query().Where(freesubscription.ScopeEQ(scope)).All(ctx)
			if err != nil {
				return err
			}
			for _, sub := range subs {
				if err := c.FreeDelivery.Update().Where(freedelivery.SubscriptionIDEQ(sub.ID), freedelivery.StatusIn(freedelivery.StatusPending, freedelivery.StatusRetry)).SetDestinationID(destination).Exec(ctx); err != nil {
					return err
				}
			}

			if err := c.FreeSubscription.Update().Where(freesubscription.ScopeEQ(scope)).SetDestinationID(destination).SetManagedBy(manager).Exec(ctx); err != nil {
				return err
			}
		}

		for _, source := range validated {
			if !enabled {
				sub, err := c.FreeSubscription.Query().Where(freesubscription.ScopeEQ(scope), freesubscription.SourceEQ(source)).Only(ctx)
				if ent.IsNotFound(err) {
					continue
				}

				if err != nil {
					return err
				}

				if err := c.FreeSubscription.UpdateOne(sub).SetEnabled(false).SetManagedBy(manager).Exec(ctx); err != nil {
					return err
				}

				if err := c.FreeDelivery.Update().Where(freedelivery.SubscriptionIDEQ(sub.ID), freedelivery.StatusIn(freedelivery.StatusPending, freedelivery.StatusRetry, freedelivery.StatusDead)).SetStatus(freedelivery.StatusCancelled).Exec(ctx); err != nil {
					return err
				}
				continue
			}

			if err := c.FreeSubscription.Create().SetScope(scope).SetSource(source).SetDestinationKind(kind).SetDestinationID(destination).SetManagedBy(manager).OnConflictColumns(freesubscription.FieldScope, freesubscription.FieldSource).Update(func(u *ent.FreeSubscriptionUpsert) {
				u.SetEnabled(true).SetDestinationID(destination).SetManagedBy(manager).SetUpdatedAt(time.Now())
			}).Exec(ctx); err != nil {
				return err
			}

			sub, err := c.FreeSubscription.Query().Where(freesubscription.ScopeEQ(scope), freesubscription.SourceEQ(source)).Only(ctx)
			if err != nil {
				return err
			}
			age := 2 * time.Hour
			if source == "ubisoft" {
				age = 8 * time.Hour
			}
			offers, err := c.FreeOffer.Query().Where(freeoffer.SourceEQ(source), freeoffer.Eligible(true), freeoffer.LastSeenAtGT(time.Now().Add(-age))).All(ctx)
			if err != nil {
				return err
			}
			for _, offer := range offers {
				if offer.Payload.Publishable(time.Now()) {
					if err := queueFree(ctx, c, offer, sub); err != nil {
						return err
					}
				}
			}
		}

		return nil
	})
}

func (s *Store) FreeMonitors(ctx context.Context) ([]*ent.FreeMonitor, error) {
	return s.Client.FreeMonitor.Query().Order(ent.Asc(freemonitor.FieldID)).All(ctx)
}

func (s *Store) InitializeFreeMonitors(ctx context.Context) error {
	for _, source := range freegames.Sources() {
		if err := s.Client.FreeMonitor.Create().SetID(source).OnConflictColumns(freemonitor.FieldID).Ignore().Exec(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) RecordFreeScan(ctx context.Context, source string, result freegames.Result, next time.Time) error {
	return s.write(ctx, func(c *ent.Client) error {
		seen := make([]string, 0, len(result.Offers))
		for _, payload := range result.Offers {
			if payload.Source != source || payload.ProductID == "" || payload.Campaign == "" {
				continue
			}

			eligible := payload.Publishable(time.Now())
			identity := payload.Key()
			seen = append(seen, identity)
			previous, err := c.FreeOffer.Query().Where(freeoffer.IdentityEQ(identity)).Only(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return err
			}

			if err := c.FreeOffer.Create().SetIdentity(identity).SetSource(source).SetPayload(payload).SetEligible(eligible).OnConflictColumns(freeoffer.FieldIdentity).Update(func(u *ent.FreeOfferUpsert) {
				u.SetPayload(payload).SetEligible(eligible).SetLastSeenAt(time.Now())
				if eligible {
					u.SetClosed(false)
					if previous != nil && previous.Closed {
						u.AddGeneration(1)
					}
				}
			}).Exec(ctx); err != nil {
				return err
			}

			if !eligible {
				continue
			}

			offer, err := c.FreeOffer.Query().Where(freeoffer.IdentityEQ(identity)).Only(ctx)
			if err != nil {
				return err
			}
			subs, err := c.FreeSubscription.Query().Where(freesubscription.SourceEQ(source), freesubscription.Enabled(true)).All(ctx)
			if err != nil {
				return err
			}
			for _, sub := range subs {
				if err := queueFree(ctx, c, offer, sub); err != nil {
					return err
				}
			}
		}

		status := "ok"
		update := c.FreeMonitor.UpdateOneID(source).SetCheckedAt(time.Now()).SetNextCheckAt(next).SetProblems(result.Problems)
		if len(result.Problems) > 0 {
			status = "degraded"
			update.AddFailures(1)
		} else {
			update.SetFailures(0)
			// An unavailable branch never invalidates the last successful observation.
			if err := c.FreeOffer.Update().Where(freeoffer.SourceEQ(source), freeoffer.IdentityNotIn(seen...)).SetEligible(false).SetClosed(true).Exec(ctx); err != nil {
				return err
			}
		}

		return update.SetStatus(status).Exec(ctx)
	})
}

func (s *Store) RecoverFree(ctx context.Context, startup bool) error {
	q := s.Client.FreeDelivery.Update().Where(freedelivery.StatusEQ(freedelivery.StatusSending))
	if !startup {
		q.Where(freedelivery.UpdatedAtLT(time.Now().Add(-5 * time.Minute)))
	}

	return q.SetStatus(freedelivery.StatusRetry).SetNextAttemptAt(time.Now()).Exec(ctx)
}

func (s *Store) ClaimFree(ctx context.Context) (*ent.FreeDelivery, error) {
	var result *ent.FreeDelivery
	err := s.write(ctx, func(c *ent.Client) error {
		d, err := c.FreeDelivery.Query().Where(freedelivery.StatusIn(freedelivery.StatusPending, freedelivery.StatusRetry), freedelivery.NextAttemptAtLTE(time.Now())).Order(ent.Asc(freedelivery.FieldNextAttemptAt)).ForUpdate(entsql.WithLockAction(entsql.SkipLocked)).First(ctx)
		if ent.IsNotFound(err) {
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
		if d.Payload.Source == "ubisoft" {
			maxAge = 8 * time.Hour
		}

		if !sub.Enabled || !offer.Eligible || offer.Generation != d.OfferGeneration || !offer.Payload.Publishable(time.Now()) {
			return c.FreeDelivery.UpdateOne(d).SetStatus(freedelivery.StatusCancelled).SetLastError("subscription disabled or offer no longer eligible").Exec(ctx)
		}

		if time.Since(offer.LastSeenAt) > maxAge {
			if time.Since(d.CreatedAt) > 72*time.Hour {
				return c.FreeDelivery.UpdateOne(d).SetStatus(freedelivery.StatusCancelled).SetLastError("fresh confirmation unavailable within 72 hours").Exec(ctx)
			}

			return c.FreeDelivery.UpdateOne(d).SetStatus(freedelivery.StatusRetry).SetNextAttemptAt(time.Now().Add(time.Hour)).SetLastError("waiting for fresh offer confirmation").Exec(ctx)
		}

		result, err = c.FreeDelivery.UpdateOne(d).SetStatus(freedelivery.StatusSending).SetDestinationID(sub.DestinationID).SetPayload(offer.Payload).AddAttemptCount(1).Save(ctx)

		return err
	})

	return result, err
}

func (s *Store) FinishFree(ctx context.Context, id string, status freedelivery.Status, retryAt time.Time, message, reason string) error {
	return s.Client.FreeDelivery.Update().Where(freedelivery.IDEQ(id), freedelivery.StatusEQ(freedelivery.StatusSending)).SetStatus(status).SetNextAttemptAt(retryAt).SetMessageID(message).SetLastError(reason).Exec(ctx)
}

func (s *Store) AdministerFree(ctx context.Context, id string, retry bool) error {
	q := s.Client.FreeDelivery.Update().Where(freedelivery.IDEQ(id))
	if retry {
		q.Where(freedelivery.StatusEQ(freedelivery.StatusDead)).SetStatus(freedelivery.StatusRetry).SetNextAttemptAt(time.Now()).SetLastError("")
	} else {
		q.Where(freedelivery.StatusIn(freedelivery.StatusPending, freedelivery.StatusRetry, freedelivery.StatusDead)).SetStatus(freedelivery.StatusCancelled)
	}

	n, err := q.Save(ctx)
	if err == nil && n == 0 {
		return errors.New("no free-game delivery in an eligible state was found")
	}

	return err
}
