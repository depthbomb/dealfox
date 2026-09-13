package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store/models"
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
	count, err := s.Client.App.Query().Where(models.AppColumns.HasTargetsWith(models.TargetColumns.HasRulesWith(models.RuleColumns.Enabled.Eq(true)))).Count(ctx)

	return int(count), err
}

func (s *Store) Add(ctx context.Context, req AddRequest) (*models.Rule, error) {
	var result *models.Rule
	err := s.write(ctx, func(c *models.Client) error {
		if req.RequestID != "" {
			previous, err := c.Rule.Query().Where(models.RuleColumns.RequestID.Eq(req.RequestID), models.RuleColumns.OwnerID.Eq(req.OwnerID)).Only(ctx)
			if err == nil {
				result = previous

				return nil
			}

			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}

		count, err := c.Rule.Query().Where(models.RuleColumns.OwnerID.Eq(req.OwnerID), models.RuleColumns.Enabled.Eq(true)).Count(ctx)
		if err != nil {
			return err
		}

		if count >= int64(req.Maximum) {
			return &domain.PublicError{
				Code:    "QUOTA_EXCEEDED",
				Message: fmt.Sprintf("You can have at most %d active tracking rules.", req.Maximum),
			}
		}

		p := req.Price
		if _, err := c.App.Create().SetID(p.AppID).SetName(p.Name).SetNameFold(strings.ToLower(p.Name)).SetType(p.Type).SetUpdatedAt(time.Now()).OnConflict(models.AppColumns.ID).UpdateExcluded(models.AppColumns.Name, models.AppColumns.NameFold, models.AppColumns.Type, models.AppColumns.UpdatedAt).Save(ctx); err != nil {
			return err
		}

		if _, err := c.Target.Create().SetAppID(p.AppID).SetCountry(p.Country).OnConflict(models.TargetColumns.AppID, models.TargetColumns.Country).DoNothing().Save(ctx); err != nil {
			return err
		}

		t, err := c.Target.Query().Where(models.TargetColumns.AppID.Eq(p.AppID), models.TargetColumns.Country.Eq(p.Country)).Only(ctx)
		if err != nil {
			return err
		}

		exists, err := c.Rule.Query().Where(models.RuleColumns.OwnerID.Eq(req.OwnerID), models.RuleColumns.TargetID.Eq(t.ID), models.RuleColumns.Condition.Eq(models.RuleCondition(req.Condition)), models.RuleColumns.Enabled.Eq(true)).Exists(ctx)
		if err != nil {
			return err
		}

		if exists {
			return &domain.PublicError{
				Code:    "CONFLICT",
				Message: "You are already tracking this Steam item and condition.",
			}
		}

		create := c.Rule.Create().SetTargetID(t.ID).SetOwnerID(req.OwnerID).SetCondition(models.RuleCondition(req.Condition)).SetRecurring(req.Recurring)
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

func (s *Store) List(ctx context.Context, owner string) ([]*models.Rule, error) {
	return s.Client.Rule.Query().Where(models.RuleColumns.OwnerID.Eq(owner), models.RuleColumns.Enabled.Eq(true)).WithTarget(func(q models.TargetQuery) models.TargetQuery {
		return q.WithApp()
	}).OrderBy(models.RuleColumns.CreatedAt.Asc()).All(ctx)
}

func (s *Store) Remove(ctx context.Context, owner, id string) error {
	return s.write(ctx, func(c *models.Client) error {
		n, err := c.Rule.Update().Where(models.RuleColumns.ID.Eq(id), models.RuleColumns.OwnerID.Eq(owner), models.RuleColumns.Enabled.Eq(true)).SetEnabled(false).Exec(ctx)
		if err == nil && n == 0 {
			return &domain.PublicError{
				Code:    "NOT_FOUND",
				Message: "That active tracking rule was not found.",
			}
		}

		return err
	})
}
