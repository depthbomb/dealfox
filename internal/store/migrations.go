package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/depthbomb/dealfox/migrations"
)

func (s *Store) ValidateMigrations(ctx context.Context) error {
	expected, err := migrations.Hashes()
	if err != nil {
		return err
	}

	// Atlas uses a dedicated revision schema for database-scoped URLs and the
	// selected schema for schema-scoped URLs. Support both layouts.
	var localHistory bool
	if err := s.db.QueryRowContext(ctx, "SELECT to_regclass('atlas_schema_revisions') IS NOT NULL").Scan(&localHistory); err != nil {
		return err
	}

	history := "atlas_schema_revisions.atlas_schema_revisions"
	if localHistory {
		history = "atlas_schema_revisions"
	}
	rows, err := s.db.QueryContext(ctx, "SELECT version, hash, applied, total, error FROM "+history+" ORDER BY version")
	if err != nil {
		return errors.New("migration history is unavailable; run `dealfox database migrate` against an empty Go DealFox database")
	}
	defer rows.Close()

	for rows.Next() {
		var version, hash string
		var applied, total int
		var failure sql.NullString
		if err := rows.Scan(&version, &hash, &applied, &total, &failure); err != nil {
			return err
		}

		if expected[version] != hash || applied != total || (failure.Valid && failure.String != "") {
			return fmt.Errorf("migration %s is changed, incomplete, or newer than this binary", version)
		}

		delete(expected, version)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	if len(expected) != 0 {
		return errors.New("database has pending migrations; run `dealfox database migrate`")
	}

	return nil
}
