package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/steam"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/urfave/cli/v3"
)

type configAction func(context.Context, *cli.Command, *config.Config) error
type storeAction func(context.Context, *cli.Command, *config.Config, *store.Store) error

func withConfig(action configAction) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Args().Len() != 0 {
			return fmt.Errorf("unexpected argument %q", cmd.Args().First())
		}

		cfg, err := loadConfig(cmd.String("config-dir"))
		if err != nil {
			return err
		}

		return action(ctx, cmd, &cfg)
	}
}

func withStore(action storeAction) cli.ActionFunc {
	return withConfig(func(ctx context.Context, cmd *cli.Command, cfg *config.Config) error {
		if cfg.DatabaseURL == nil {
			return errors.New("DATABASE_URL is required")
		}

		db, err := store.Open(ctx, cfg.DatabaseURL.Release())
		if err != nil {
			return err
		}
		defer db.Close()

		if err := db.ValidateMigrations(ctx); err != nil {
			return err
		}

		return action(ctx, cmd, cfg, db)
	})
}

func New(output io.Writer, logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:           "dealfox",
		Usage:          "Steam sale alerts and free-to-keep game notifications",
		Description:    "Configuration uses envschema. Set DATABASE_URL (postgres:// syntax), BOT_TOKEN, and STEAM_WEB_API_KEY for serve. Process environment overrides .env and .env.local next to the executable.",
		Writer:         output,
		ErrWriter:      output,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags: []cli.Flag{&cli.StringFlag{
			Name:  "config-dir",
			Usage: "Directory containing .env and .env.local; defaults to the executable directory",
		}},
		Commands: []*cli.Command{
			{
				Name:  "serve",
				Usage: "Start the Discord bot and background workers",
				Action: withStore(func(ctx context.Context, _ *cli.Command, cfg *config.Config, db *store.Store) error {
					return serve(ctx, cfg, db, logger)
				}),
			},
			databaseCommand(),
			{
				Name:  "catalog",
				Usage: "Maintain the Steam game and DLC catalog",
				Commands: []*cli.Command{{
					Name:  "sync",
					Usage: "Synchronize the Steam catalog",
					Action: withStore(func(ctx context.Context, _ *cli.Command, cfg *config.Config, db *store.Store) error {
						return steam.New(cfg).Catalog(ctx, db.UpsertCatalog)
					}),
				}},
			},
			publicationCommand(),
			deliveryCommands(),
			freeGamesCommands(),
		},
	}
}
