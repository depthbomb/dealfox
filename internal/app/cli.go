package app

import (
	"errors"
	"io"
	"log/slog"

	"github.com/depthbomb/argon"
	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/steam"
	"github.com/depthbomb/dealfox/internal/store"
)

type configAction func(*argon.Context, *config.Config) error
type storeAction func(*argon.Context, *config.Config, *store.Store) error

func withConfig(action configAction) argon.Handler {
	return func(cx *argon.Context) error {
		cfg, err := loadConfig(cx.Invocation.String("config-dir"))
		if err != nil {
			return err
		}

		return action(cx, &cfg)
	}
}

func withStore(action storeAction) argon.Handler {
	return withConfig(func(cx *argon.Context, cfg *config.Config) error {
		if cfg.DatabasePath == "" {
			return errors.New("DATABASE_PATH is required")
		}

		db, err := store.Open(cx.Context, cfg.DatabasePath)
		if err != nil {
			return err
		}
		defer db.Close()

		if err := db.ValidateMigrations(cx.Context); err != nil {
			return err
		}

		return action(cx, cfg, db)
	})
}

// New registers commands without loading configuration. Callers supply help text.
func New(output io.Writer, logger *slog.Logger) *argon.App {
	application := argon.New("dealfox")
	application.IO.Out = output
	application.Root.Summary = "Steam sale alerts and free-to-keep game notifications"
	application.Root.Description = "Configuration uses envschema. Set DATABASE_PATH (local SQLite file path), BOT_TOKEN, and STEAM_WEB_API_KEY for serve. Process environment overrides .env and .env.local next to the executable."
	application.Options = []*argon.Option{
		argon.StringOption(argon.Option{
			Name:  "config-dir",
			Usage: "Directory containing .env and .env.local; defaults to the executable directory",
		}),
	}
	application.Root.Commands = []*argon.Command{
		{
			Name:    "serve",
			Summary: "Start the Discord bot and background workers",
			Run: withStore(func(cx *argon.Context, cfg *config.Config, db *store.Store) error {
				return serve(cx.Context, cfg, db, logger)
			}),
		},
		databaseCommand(),
		{
			Name:    "catalog",
			Summary: "Maintain the Steam game and DLC catalog",
			Commands: []*argon.Command{{
				Name:    "sync",
				Summary: "Synchronize the Steam catalog",
				Run: withStore(func(cx *argon.Context, cfg *config.Config, db *store.Store) error {
					return steam.New(cfg).Catalog(cx.Context, db.UpsertCatalog)
				}),
			}},
		},
		publicationCommand(),
		deliveryCommands(),
		freeGamesCommands(),
	}

	return application
}
