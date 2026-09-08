package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/depthbomb/dealfox/ent"
	"github.com/depthbomb/dealfox/ent/freedelivery"
	"github.com/depthbomb/dealfox/ent/freeoffer"
	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/urfave/cli/v3"
)

func freeGamesCommands() *cli.Command {
	return &cli.Command{
		Name:  "freegames",
		Usage: "Inspect free-game sources and maintain their delivery queue",
		Commands: []*cli.Command{
			{
				Name:  "scan",
				Usage: "Check first-party sources without saving offers or sending notifications",
				Flags: []cli.Flag{&cli.StringFlag{
					Name:  "sources",
					Value: "all",
					Usage: "steam, epic, gog, ubisoft, comma-separated selection, or all",
				}},
				Action: withConfig(func(ctx context.Context, cmd *cli.Command, _ *config.Config) error {
					sources, err := freegames.ParseSources(cmd.String("sources"))
					if err != nil {
						return err
					}
					client := freegames.NewClient()
					for _, source := range sources {
						if err := json.NewEncoder(cmd.Writer).Encode(client.Scan(ctx, source)); err != nil {
							return err
						}
					}

					return nil
				}),
			},
			{
				Name:  "status",
				Usage: "Show source health, quarantined candidates, and failed deliveries",
				Action: withStore(func(ctx context.Context, cmd *cli.Command, _ *config.Config, db *store.Store) error {
					monitors, err := db.FreeMonitors(ctx)
					if err != nil {
						return err
					}
					for _, monitor := range monitors {
						fmt.Fprintf(cmd.Writer, "%s: %s; next %s; problems %v\n", monitor.ID, monitor.Status, monitor.NextCheckAt, monitor.Problems)
					}
					offers, err := db.Client.FreeOffer.Query().Where(freeoffer.Eligible(false)).Order(ent.Desc(freeoffer.FieldLastSeenAt)).Limit(25).All(ctx)
					if err != nil {
						return err
					}
					for _, offer := range offers {
						fmt.Fprintf(cmd.Writer, "held %s %s: %s\n", offer.ID, offer.Payload.Title, offer.Payload.Reason)
					}
					deliveries, err := db.Client.FreeDelivery.Query().Where(freedelivery.StatusEQ(freedelivery.StatusDead)).Order(ent.Desc(freedelivery.FieldUpdatedAt)).Limit(25).All(ctx)
					if err != nil {
						return err
					}
					for _, delivery := range deliveries {
						fmt.Fprintf(cmd.Writer, "dead %s: %s\n", delivery.ID, delivery.LastError)
					}

					return nil
				}),
			},
			{
				Name:  "delivery",
				Usage: "Retry or cancel one free-game delivery",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "id",
						Required: true,
					},
					&cli.BoolFlag{
						Name:  "retry",
						Usage: "Retry a dead delivery; otherwise cancel it",
					},
				},
				Action: withStore(func(ctx context.Context, cmd *cli.Command, _ *config.Config, db *store.Store) error {
					return db.AdministerFree(ctx, cmd.String("id"), cmd.Bool("retry"))
				}),
			},
		},
	}
}
