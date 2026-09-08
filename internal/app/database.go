package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/database"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/urfave/cli/v3"
)

func databaseCommand() *cli.Command {
	return &cli.Command{
		Name:  "database",
		Usage: "Create the database, apply Atlas migrations, or verify migration history",
		Commands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Create the configured PostgreSQL database if absent",
				Action: withConfig(func(ctx context.Context, _ *cli.Command, cfg *config.Config) error {
					if cfg.DatabaseURL == nil {
						return errors.New("DATABASE_URL is required")
					}

					return database.Create(ctx, cfg.DatabaseURL.Release())
				}),
			},
			{
				Name:  "migrate",
				Usage: "Apply embedded versioned migrations using the Atlas CLI",
				Action: withConfig(func(ctx context.Context, cmd *cli.Command, cfg *config.Config) error {
					if cfg.DatabaseURL == nil {
						return errors.New("DATABASE_URL is required")
					}

					return database.Migrate(ctx, cfg.DatabaseURL.Release(), cmd.Root().Writer)
				}),
			},
			{
				Name:  "status",
				Usage: "Verify applied migration versions and checksums",
				Action: withStore(func(_ context.Context, cmd *cli.Command, _ *config.Config, _ *store.Store) error {
					_, err := fmt.Fprintln(cmd.Root().Writer, "Database migrations are current and verified.")

					return err
				}),
			},
		},
	}
}
