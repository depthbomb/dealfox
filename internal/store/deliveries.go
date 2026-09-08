package store

import (
	"context"
	"errors"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/delivery"
)

func (s *Store) Recover(ctx context.Context, startup bool) error {
	q := s.Client.Delivery.Update().Where(delivery.StatusEQ(delivery.StatusSending))
	if !startup {
		q.Where(delivery.UpdatedAtLT(time.Now().Add(-5 * time.Minute)))
	}

	return q.SetStatus(delivery.StatusRetry).SetNextAttemptAt(time.Now()).SetLastError("recovered abandoned delivery").Exec(ctx)
}

func (s *Store) Claim(ctx context.Context) (*ent.Delivery, error) {
	var result *ent.Delivery
	err := s.write(ctx, func(c *ent.Client) error {
		d, err := c.Delivery.Query().Where(delivery.StatusIn(delivery.StatusPending, delivery.StatusRetry), delivery.NextAttemptAtLTE(time.Now())).Order(ent.Asc(delivery.FieldNextAttemptAt)).ForUpdate(entsql.WithLockAction(entsql.SkipLocked)).First(ctx)
		if ent.IsNotFound(err) {
			return nil
		}

		if err != nil {
			return err
		}

		if err := c.Delivery.UpdateOne(d).SetStatus(delivery.StatusSending).AddAttemptCount(1).Exec(ctx); err != nil {
			return err
		}

		result, err = c.Delivery.Query().Where(delivery.IDEQ(d.ID)).WithEvent().Only(ctx)

		return err
	})

	return result, err
}

func (s *Store) Finish(ctx context.Context, id string, status delivery.Status, retryAt time.Time, message, reason string) error {
	return s.Client.Delivery.Update().Where(delivery.IDEQ(id), delivery.StatusEQ(delivery.StatusSending)).SetStatus(status).SetNextAttemptAt(retryAt).SetMessageID(message).SetLastError(reason).Exec(ctx)
}

func (s *Store) Dead(ctx context.Context, limit int) ([]*ent.Delivery, error) {
	return s.Client.Delivery.Query().Where(delivery.StatusEQ(delivery.StatusDead)).Order(ent.Asc(delivery.FieldUpdatedAt)).Limit(limit).All(ctx)
}

func (s *Store) Administer(ctx context.Context, id string, retry bool) error {
	q := s.Client.Delivery.Update().Where(delivery.IDEQ(id))
	if retry {
		q.Where(delivery.StatusEQ(delivery.StatusDead)).SetStatus(delivery.StatusRetry).SetNextAttemptAt(time.Now()).SetLastError("")
	} else {
		q.Where(delivery.StatusIn(delivery.StatusPending, delivery.StatusRetry, delivery.StatusDead)).SetStatus(delivery.StatusCancelled)
	}

	n, err := q.Save(ctx)
	if err == nil && n == 0 {
		return errors.New("no delivery in an eligible state was found")
	}

	return err
}
