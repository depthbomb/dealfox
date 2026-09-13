package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store/models"
	"github.com/depthbomb/nook"
)

func apply(ctx context.Context, c *models.Client, t *models.Target, p domain.Price, interval time.Duration, onlyRule string) error {
	if p.AppID != t.AppID || p.Country != t.Country {
		return errors.New("observation does not match its target")
	}

	o, err := c.Observation.Create().SetTargetID(t.ID).SetObservedAt(p.ObservedAt).SetPrice(nook.JSON[domain.Price]{Data: p}).Save(ctx)
	if err != nil {
		return err
	}

	query := c.Rule.Query().Where(models.RuleColumns.TargetID.Eq(t.ID), models.RuleColumns.Enabled.Eq(true))
	if onlyRule != "" {
		query = query.Where(models.RuleColumns.ID.Eq(onlyRule))
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
			e, err := c.Event.Create().SetRuleID(r.ID).SetObservationID(o.ID).SetPayload(nook.JSON[domain.Payload]{Data: payload}).Save(ctx)
			if err != nil {
				return err
			}

			if _, err := c.Delivery.Create().SetEventID(e.ID).SetDestinationID(r.OwnerID).Save(ctx); err != nil {
				return err
			}

			if !r.Recurring {
				update.SetEnabled(false)
			}
		}

		if _, err := update.Exec(ctx); err != nil {
			return err
		}
	}

	if onlyRule != "" {
		return nil
	}

	return discard(c.Target.UpdateOneID(t.ID).SetLastObservedAt(p.ObservedAt).SetNextDueAt(p.ObservedAt.Add(interval)).SetFailureCount(0).SetLastError("").Exec(ctx))
}

func (s *Store) Observe(ctx context.Context, id string, p domain.Price, interval time.Duration) error {
	return s.write(ctx, func(c *models.Client) error {
		t, err := c.Target.Query().Where(models.TargetColumns.ID.Eq(id)).Only(ctx)
		if err != nil {
			return err
		}

		if t.LastObservedAt != nil && !p.ObservedAt.After(*t.LastObservedAt) {
			return nil
		}

		return apply(ctx, c, t, p, interval, "")
	})
}

func (s *Store) Due(ctx context.Context, limit int) ([]*models.Target, error) {
	base := s.Client.Target.Query().Where(models.TargetColumns.NextDueAt.LTE(time.Now()), models.TargetColumns.HasRulesWith(models.RuleColumns.Enabled.Eq(true))).OrderBy(models.TargetColumns.NextDueAt.Asc())
	first, err := base.First(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return base.Where(models.TargetColumns.Country.Eq(first.Country)).WithApp().Limit(limit).All(ctx)
}

func (s *Store) TargetFailed(ctx context.Context, id string) error {
	return discard(s.Client.Target.UpdateOneID(id).SetNextDueAt(time.Now().Add(5 * time.Minute)).AddFailureCount(1).SetLastError("Steam request failed").Exec(ctx))
}
