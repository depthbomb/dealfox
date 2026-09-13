package store

import (
	"context"
	"errors"

	"github.com/depthbomb/dealfox/migrations"
	"github.com/depthbomb/nook/migrate"
)

func (s *Store) ValidateMigrations(ctx context.Context) error {
	history, err := migrate.Load(migrations.Files)
	if err != nil {
		return err
	}
	status, err := migrate.Inspect(ctx, s.db.DB, history)
	if err != nil {
		return err
	}
	for _, migration := range status {
		if !migration.Applied {
			return errors.New("database has pending migrations; run dealfox database migrate")
		}
	}

	return nil
}
