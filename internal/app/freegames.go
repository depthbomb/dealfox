package app

import (
	"encoding/json"
	"fmt"

	"github.com/depthbomb/argon"
	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/store/models"
)

func freeGamesCommands() *argon.Command {
	return &argon.Command{
		Name:    "freegames",
		Summary: "Inspect free-game sources and maintain their delivery queue",
		Commands: []*argon.Command{
			{
				Name:    "scan",
				Summary: "Check first-party sources without saving offers or sending notifications",
				Options: []*argon.Option{argon.StringOption(argon.Option{
					Name:    "sources",
					Default: "all",
					Usage:   "steam, epic, gog, ubisoft, comma-separated selection, or all",
				})},
				Run: withConfig(func(cx *argon.Context, _ *config.Config) error {
					sources, err := freegames.ParseSources(cx.Invocation.String("sources"))
					if err != nil {
						return err
					}
					client := freegames.NewClient()
					for _, source := range sources {
						if err := json.NewEncoder(cx.IO.Out).Encode(client.Scan(cx.Context, source)); err != nil {
							return err
						}
					}

					return nil
				}),
			},
			{
				Name:    "status",
				Summary: "Show source health, quarantined candidates, and failed deliveries",
				Run: withStore(func(cx *argon.Context, _ *config.Config, db *store.Store) error {
					monitors, err := db.FreeMonitors(cx.Context)
					if err != nil {
						return err
					}
					for _, monitor := range monitors {
						fmt.Fprintf(cx.IO.Out, "%s: %s; next %s; problems %v\n", monitor.ID, monitor.Status, monitor.NextCheckAt, monitor.Problems)
					}
					offers, err := db.Client.FreeOffer.Query().Where(models.FreeOfferColumns.Eligible.Eq(false)).OrderBy(models.FreeOfferColumns.LastSeenAt.Desc()).Limit(25).All(cx.Context)
					if err != nil {
						return err
					}
					for _, offer := range offers {
						fmt.Fprintf(cx.IO.Out, "held %s %s: %s\n", offer.ID, offer.Payload.Data.Title, offer.Payload.Data.Reason)
					}
					deliveries, err := db.Client.FreeDelivery.Query().Where(models.FreeDeliveryColumns.Status.Eq(models.FreeDeliveryStatusDead)).OrderBy(models.FreeDeliveryColumns.UpdatedAt.Desc()).Limit(25).All(cx.Context)
					if err != nil {
						return err
					}
					for _, delivery := range deliveries {
						fmt.Fprintf(cx.IO.Out, "dead %s: %s\n", delivery.ID, delivery.LastError)
					}

					return nil
				}),
			},
			{
				Name:    "delivery",
				Summary: "Retry or cancel one free-game delivery",
				Options: []*argon.Option{
					argon.StringOption(argon.Option{
						Name:     "id",
						Required: true,
					}),
					argon.BoolOption(argon.Option{
						Name:  "retry",
						Usage: "Retry a dead delivery; otherwise cancel it",
					}),
				},
				Run: withStore(func(cx *argon.Context, _ *config.Config, db *store.Store) error {
					return db.AdministerFree(cx.Context, cx.Invocation.String("id"), cx.Invocation.Bool("retry"))
				}),
			},
		},
	}
}
