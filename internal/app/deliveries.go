package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/urfave/cli/v3"
)

func deliveryAction(retry bool) cli.ActionFunc {
	return withStore(func(ctx context.Context, cmd *cli.Command, _ *config.Config, db *store.Store) error {
		id := cmd.String("id")
		if !cuid.IsValidLength(id, 24) {
			return errors.New("id must be a 24-character delivery CUID")
		}

		return db.Administer(ctx, id, retry)
	})
}

func deliveryIDFlag() cli.Flag {
	return &cli.StringFlag{
		Name:     "id",
		Usage:    "Delivery CUID",
		Required: true,
	}
}

func deliveryCommands() *cli.Command {
	return &cli.Command{
		Name:  "deliveries",
		Usage: "Inspect and administer notification delivery failures",
		Commands: []*cli.Command{
			{
				Name:  "list-dead",
				Usage: "List dead-letter deliveries",
				Flags: []cli.Flag{&cli.IntFlag{
					Name:  "limit",
					Value: 50,
					Usage: "Maximum number to list (1-1000)",
				}},
				Action: withStore(func(ctx context.Context, cmd *cli.Command, _ *config.Config, db *store.Store) error {
					limit := cmd.Int("limit")
					if limit < 1 || limit > 1000 {
						return errors.New("limit must be between 1 and 1000")
					}

					rows, err := db.Dead(ctx, limit)
					if err != nil {
						return err
					}

					for _, row := range rows {
						fmt.Fprintf(cmd.Root().Writer, "%s\tattempts=%d\t%s\t%s\n", row.ID, row.AttemptCount, row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"), row.LastError)
					}

					return nil
				}),
			},
			{
				Name:   "retry",
				Usage:  "Retry a dead delivery",
				Flags:  []cli.Flag{deliveryIDFlag()},
				Action: deliveryAction(true),
			},
			{
				Name:   "cancel",
				Usage:  "Cancel a pending, retrying, or dead delivery",
				Flags:  []cli.Flag{deliveryIDFlag()},
				Action: deliveryAction(false),
			},
		},
	}
}
