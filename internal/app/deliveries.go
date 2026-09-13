package app

import (
	"errors"
	"fmt"

	"github.com/depthbomb/argon"
	cuid "github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/store"
)

func deliveryAction(retry bool) argon.Handler {
	return withStore(func(cx *argon.Context, _ *config.Config, db *store.Store) error {
		id := cx.Invocation.String("id")
		if !cuid.IsValidLength(id, 24) {
			return errors.New("id must be a 24-character delivery CUID")
		}

		return db.Administer(cx.Context, id, retry)
	})
}

func deliveryIDOption() *argon.Option {
	return argon.StringOption(argon.Option{
		Name:     "id",
		Usage:    "Delivery CUID",
		Required: true,
	})
}

func deliveryCommands() *argon.Command {
	return &argon.Command{
		Name:    "deliveries",
		Summary: "Inspect and administer notification delivery failures",
		Commands: []*argon.Command{
			{
				Name:    "list-dead",
				Summary: "List dead-letter deliveries",
				Options: []*argon.Option{argon.IntOption(argon.Option{
					Name:    "limit",
					Default: 50,
					Usage:   "Maximum number to list (1-1000)",
				})},
				Validate: func(inv *argon.Invocation) error {
					limit := inv.Int("limit")
					if limit < 1 || limit > 1000 {
						return errors.New("limit must be between 1 and 1000")
					}

					return nil
				},
				Run: withStore(func(cx *argon.Context, _ *config.Config, db *store.Store) error {
					rows, err := db.Dead(cx.Context, cx.Invocation.Int("limit"))
					if err != nil {
						return err
					}

					for _, row := range rows {
						fmt.Fprintf(cx.IO.Out, "%s\tattempts=%d\t%s\t%s\n", row.ID, row.AttemptCount, row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"), row.LastError)
					}

					return nil
				}),
			},
			{
				Name:    "retry",
				Summary: "Retry a dead delivery",
				Options: []*argon.Option{deliveryIDOption()},
				Run:     deliveryAction(true),
			},
			{
				Name:    "cancel",
				Summary: "Cancel a pending, retrying, or dead delivery",
				Options: []*argon.Option{deliveryIDOption()},
				Run:     deliveryAction(false),
			},
		},
	}
}
