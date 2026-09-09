package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/discord/commands"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	tomogocommands "github.com/depthbomb/tomogo/commands"
	"github.com/depthbomb/tomogo/rest"
	"github.com/tomogo-framework/snowflake"
	"github.com/urfave/cli/v3"
)

func publish(ctx context.Context, cfg *config.Config, guild string, confirm bool, output io.Writer) error {
	if cfg.BotToken == nil {
		return errors.New("BOT_TOKEN is required")
	}

	var guildID api.ID
	var err error
	if guild != "" {
		guildID, err = snowflake.Parse(guild)
		if err != nil || guildID == 0 {
			return errors.New("guild must be a positive Discord snowflake")
		}
	}

	client := rest.New(rest.Config{
		Token: cfg.BotToken.Release(),
	})
	applicationID, err := applicationID(ctx, cfg, client)
	if err != nil {
		return err
	}

	commandHandler := commands.Handler{
		Tracker: &tracker.Service{
			Config: cfg,
		},
	}
	definitions, err := commandHandler.Definitions()
	if err != nil {
		return err
	}

	if guildID != 0 {
		definitions, err = tomogocommands.ForGuild(definitions)
		if err != nil {
			return err
		}

		if confirm {
			_, _, err := client.Commands().ExplicitlyReplaceAllGuild(ctx, applicationID, guildID, definitions)

			return err
		}

		comparison, _, err := client.Commands().CompareRemoteGuild(ctx, applicationID, guildID, definitions)
		if err != nil {
			return err
		}

		for _, entry := range comparison.Entries {
			fmt.Fprintf(output, "%s %s\n", entry.State, entry.Name)
		}

		return nil
	}

	if confirm {
		_, _, err := client.Commands().ExplicitlyReplaceAllGlobal(ctx, applicationID, definitions)

		return err
	}

	comparison, _, err := client.Commands().CompareRemoteGlobal(ctx, applicationID, definitions)
	if err != nil {
		return err
	}

	for _, entry := range comparison.Entries {
		fmt.Fprintf(output, "%s %s\n", entry.State, entry.Name)
	}

	return nil
}

func publicationCommand() *cli.Command {
	return &cli.Command{
		Name:  "commands",
		Usage: "Compare slash commands, or replace the selected scope with --confirm",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "guild",
				Usage: "Development guild ID; omit for global commands",
			},
			&cli.BoolFlag{
				Name:  "confirm",
				Usage: "Replace all commands in the selected scope",
			},
		},
		Action: withConfig(func(ctx context.Context, cmd *cli.Command, cfg *config.Config) error {
			return publish(ctx, cfg, cmd.String("guild"), cmd.Bool("confirm"), cmd.Root().Writer)
		}),
	}
}
