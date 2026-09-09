package store

import (
	"context"
	"fmt"
	"time"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/app"
	"github.com/depthbomb/dealfox/ent/rule"
	"github.com/depthbomb/dealfox/ent/target"
	"github.com/depthbomb/dealfox/internal/domain"
)

type AddRequest struct {
	OwnerID   string
	RequestID string
	Price     domain.Price
	Condition string
	Budget    *domain.Money
	Recurring bool
	Maximum   int
	Interval  time.Duration
}

// TrackedGameCount counts distinct Steam apps with at least one enabled rule.
func (s *Store) TrackedGameCount(ctx context.Context) (int, error) {
	return s.Client.App.Query().Where(app.HasTargetsWith(target.HasRulesWith(rule.Enabled(true)))).Count(ctx)
}

func (s *Store) Add(ctx context.Context, req AddRequest) (*ent.Rule, error) {
	var result *ent.Rule
	err := s.write(ctx, func(c *ent.Client) error {
		if req.RequestID != "" {
			previous, err := c.Rule.Query().Where(rule.RequestIDEQ(req.RequestID), rule.OwnerIDEQ(req.OwnerID)).Only(ctx)
			if err == nil {
				result = previous

				return nil
			}

			if !ent.IsNotFound(err) {
				return err
			}
		}

		count, err := c.Rule.Query().Where(rule.OwnerIDEQ(req.OwnerID), rule.Enabled(true)).Count(ctx)
		if err != nil {
			return err
		}

		if count >= req.Maximum {
			return &domain.PublicError{
				Code:    "QUOTA_EXCEEDED",
				Message: fmt.Sprintf("You can have at most %d active tracking rules.", req.Maximum),
			}
		}

		p := req.Price
		if err := c.App.Create().SetID(p.AppID).SetName(p.Name).SetType(p.Type).OnConflictColumns(app.FieldID).UpdateName().UpdateType().UpdateUpdatedAt().Exec(ctx); err != nil {
			return err
		}

		if err := c.Target.Create().SetAppID(p.AppID).SetCountry(p.Country).OnConflictColumns(target.FieldAppID, target.FieldCountry).Ignore().Exec(ctx); err != nil {
			return err
		}

		t, err := c.Target.Query().Where(target.AppIDEQ(p.AppID), target.CountryEQ(p.Country)).ForUpdate().Only(ctx)
		if err != nil {
			return err
		}

		exists, err := c.Rule.Query().Where(rule.OwnerIDEQ(req.OwnerID), rule.TargetIDEQ(t.ID), rule.ConditionEQ(rule.Condition(req.Condition)), rule.Enabled(true)).Exist(ctx)
		if err != nil {
			return err
		}

		if exists {
			return &domain.PublicError{
				Code:    "CONFLICT",
				Message: "You are already tracking this Steam item and condition.",
			}
		}

		create := c.Rule.Create().SetTargetID(t.ID).SetOwnerID(req.OwnerID).SetCondition(rule.Condition(req.Condition)).SetRecurring(req.Recurring)
		if req.RequestID != "" {
			create.SetRequestID(req.RequestID)
		}

		if req.Budget != nil {
			create.SetBudgetMinor(req.Budget.Minor).SetBudgetCurrency(req.Budget.Currency)
		}

		created, err := create.Save(ctx)
		if err != nil {
			return err
		}

		// Stale cached creation data must not regress existing sale latches.
		onlyRule := ""
		if t.LastObservedAt != nil && !p.ObservedAt.After(*t.LastObservedAt) {
			onlyRule = created.ID
		}

		if err := apply(ctx, c, t, p, req.Interval, onlyRule); err != nil {
			return err
		}

		result, err = c.Rule.Get(ctx, created.ID)

		return err
	})

	return result, err
}

func (s *Store) List(ctx context.Context, owner string) ([]*ent.Rule, error) {
	return s.Client.Rule.Query().Where(rule.OwnerIDEQ(owner), rule.Enabled(true)).WithTarget(func(q *ent.TargetQuery) {
		q.WithApp()
	}).Order(ent.Asc(rule.FieldCreatedAt)).All(ctx)
}

func (s *Store) Remove(ctx context.Context, owner, id string) error {
	return s.write(ctx, func(c *ent.Client) error {
		n, err := c.Rule.Update().Where(rule.IDEQ(id), rule.OwnerIDEQ(owner), rule.Enabled(true)).SetEnabled(false).Save(ctx)
		if err == nil && n == 0 {
			return &domain.PublicError{
				Code:    "NOT_FOUND",
				Message: "That active tracking rule was not found.",
			}
		}

		return err
	})
}
