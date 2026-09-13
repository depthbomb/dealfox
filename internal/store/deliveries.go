package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/depthbomb/dealfox/internal/store/models"
)

func (s *Store) Recover(ctx context.Context, startup bool) error {
	q := s.Client.Delivery.Update().Where(models.DeliveryColumns.Status.Eq(models.DeliveryStatusSending))
	if !startup {
		q.Where(models.DeliveryColumns.UpdatedAt.LT(time.Now().Add(-5 * time.Minute)))
	}

	return discard(q.SetStatus(models.DeliveryStatusRetry).SetNextAttemptAt(time.Now()).SetLastError("recovered abandoned delivery").Exec(ctx))
}

func (s *Store) Claim(ctx context.Context) (*models.Delivery, error) {
	var result *models.Delivery
	err := s.write(ctx, func(c *models.Client) error {
		d, err := c.Delivery.Query().Where(models.DeliveryColumns.Status.In(models.DeliveryStatusPending, models.DeliveryStatusRetry), models.DeliveryColumns.NextAttemptAt.LTE(time.Now())).OrderBy(models.DeliveryColumns.NextAttemptAt.Asc()).First(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		if err != nil {
			return err
		}

		n, err := c.Delivery.UpdateOneID(d.ID).Where(models.DeliveryColumns.Status.Eq(d.Status)).SetStatus(models.DeliveryStatusSending).AddAttemptCount(1).Exec(ctx)
		if err != nil {
			return err
		}

		if n != 1 {
			return errors.New("delivery claim did not update exactly one row")
		}

		result, err = c.Delivery.Query().Where(models.DeliveryColumns.ID.Eq(d.ID)).WithEvent().Only(ctx)

		return err
	})

	return result, err
}

func (s *Store) Finish(ctx context.Context, id string, status models.DeliveryStatus, retryAt time.Time, message, reason string) error {
	return discard(s.Client.Delivery.Update().Where(models.DeliveryColumns.ID.Eq(id), models.DeliveryColumns.Status.Eq(models.DeliveryStatusSending)).SetStatus(status).SetNextAttemptAt(retryAt).SetMessageID(message).SetLastError(reason).Exec(ctx))
}

func (s *Store) Dead(ctx context.Context, limit int) ([]*models.Delivery, error) {
	return s.Client.Delivery.Query().Where(models.DeliveryColumns.Status.Eq(models.DeliveryStatusDead)).OrderBy(models.DeliveryColumns.UpdatedAt.Asc()).Limit(limit).All(ctx)
}

func (s *Store) Administer(ctx context.Context, id string, retry bool) error {
	q := s.Client.Delivery.Update().Where(models.DeliveryColumns.ID.Eq(id))
	if retry {
		q.Where(models.DeliveryColumns.Status.Eq(models.DeliveryStatusDead)).SetStatus(models.DeliveryStatusRetry).SetNextAttemptAt(time.Now()).SetLastError("")
	} else {
		q.Where(models.DeliveryColumns.Status.In(models.DeliveryStatusPending, models.DeliveryStatusRetry, models.DeliveryStatusDead)).SetStatus(models.DeliveryStatusCancelled)
	}

	n, err := q.Exec(ctx)
	if err == nil && n == 0 {
		return errors.New("no delivery in an eligible state was found")
	}

	return err
}
