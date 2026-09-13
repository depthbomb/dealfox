package app

import (
	"errors"
	"fmt"

	"github.com/depthbomb/argon"
	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/database"
	"github.com/depthbomb/dealfox/internal/store"
)

func databaseCommand() *argon.Command {
	return &argon.Command{
		Name:    "database",
		Summary: "Create the database, apply SQLite migrations, or verify migration history",
		Commands: []*argon.Command{
			{
				Name:    "create",
				Summary: "Create the configured SQLite database file if absent",
				Run: withConfig(func(cx *argon.Context, cfg *config.Config) error {
					if cfg.DatabasePath == "" {
						return errors.New("DATABASE_PATH is required")
					}

					return database.Create(cx.Context, cfg.DatabasePath)
				}),
			},
			{
				Name:    "migrate",
				Summary: "Apply embedded versioned migrations using Nook",
				Run: withConfig(func(cx *argon.Context, cfg *config.Config) error {
					if cfg.DatabasePath == "" {
						return errors.New("DATABASE_PATH is required")
					}

					return database.Migrate(cx.Context, cfg.DatabasePath, cx.IO.Out)
				}),
			},
			{
				Name:    "status",
				Summary: "Verify applied migration versions and checksums",
				Run: withStore(func(cx *argon.Context, _ *config.Config, _ *store.Store) error {
					_, err := fmt.Fprintln(cx.IO.Out, "Database migrations are current and verified.")

					return err
				}),
			},
		},
	}
}
