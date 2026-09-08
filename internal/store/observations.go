package store

import (
	"context"
	"errors"
	"time"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/rule"
	"github.com/depthbomb/dealfox/ent/target"
	"github.com/depthbomb/dealfox/internal/domain"
)

func apply(ctx context.Context, c *ent.Client, t *ent.Target, p domain.Price, interval time.Duration, onlyRule string) error {
	if p.AppID != t.AppID || p.Country != t.Country {
		return errors.New("observation does not match its target")
	}

	o, err := c.Observation.Create().SetTargetID(t.ID).SetObservedAt(p.ObservedAt).SetPrice(p).Save(ctx)
	if err != nil {
		return err
	}

	query := c.Rule.Query().Where(rule.TargetIDEQ(t.ID), rule.Enabled(true)).ForUpdate()
	if onlyRule != "" {
		query.Where(rule.IDEQ(onlyRule))
	}

	rules, err := query.All(ctx)
	if err != nil {
		return err
	}

	for _, r := range rules {
		known, qualifies := p.Evaluate(string(r.Condition), r.BudgetMinor, r.BudgetCurrency)
		if !known {
			continue
		}

		entered := qualifies && !r.Latched
		latched := qualifies || (r.Recurring && r.Latched && p.SaleState == "on_sale")
		update := c.Rule.UpdateOneID(r.ID).SetLatched(latched)
		if entered {
			if p.Regular == nil || p.Final == nil || p.Discount == nil {
				return errors.New("qualifying price is incomplete")
			}

			payload := domain.Payload{
				RuleID:    r.ID,
				AppID:     p.AppID,
				Name:      p.Name,
				Country:   p.Country,
				Currency:  p.Currency,
				Regular:   *p.Regular,
				Final:     *p.Final,
				Discount:  *p.Discount,
				Recurring: r.Recurring,
			}
			e, err := c.Event.Create().SetRuleID(r.ID).SetObservationID(o.ID).SetPayload(payload).Save(ctx)
			if err != nil {
				return err
			}

			if err := c.Delivery.Create().SetEventID(e.ID).SetDestinationID(r.OwnerID).Exec(ctx); err != nil {
				return err
			}

			if !r.Recurring {
				update.SetEnabled(false)
			}
		}

		if err := update.Exec(ctx); err != nil {
			return err
		}
	}

	if onlyRule != "" {
		return nil
	}

	return c.Target.UpdateOneID(t.ID).SetLastObservedAt(p.ObservedAt).SetNextDueAt(p.ObservedAt.Add(interval)).SetFailureCount(0).SetLastError("").Exec(ctx)
}

func (s *Store) Observe(ctx context.Context, id string, p domain.Price, interval time.Duration) error {
	return s.write(ctx, func(c *ent.Client) error {
		t, err := c.Target.Query().Where(target.IDEQ(id)).ForUpdate().Only(ctx)
		if err != nil {
			return err
		}

		if t.LastObservedAt != nil && !p.ObservedAt.After(*t.LastObservedAt) {
			return nil
		}

		return apply(ctx, c, t, p, interval, "")
	})
}

func (s *Store) Due(ctx context.Context, limit int) ([]*ent.Target, error) {
	base := s.Client.Target.Query().Where(target.NextDueAtLTE(time.Now()), target.HasRulesWith(rule.Enabled(true))).Order(ent.Asc(target.FieldNextDueAt))
	first, err := base.Clone().First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return base.Where(target.CountryEQ(first.Country)).WithApp().Limit(limit).All(ctx)
}

func (s *Store) TargetFailed(ctx context.Context, id string) error {
	return s.Client.Target.UpdateOneID(id).SetNextDueAt(time.Now().Add(5 * time.Minute)).AddFailureCount(1).SetLastError("Steam request failed").Exec(ctx)
}
