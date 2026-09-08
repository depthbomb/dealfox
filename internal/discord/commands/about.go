package commands

import (
	"context"

	"github.com/depthbomb/dealfox/internal/discord/embeds"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/interactions"
)

func (h *Handler) aboutCommand() command {
	return command{
		definition: everywhere(api.ApplicationCommand{
			Name:        "about",
			Description: "Learn about DealFox",
		}),
		handle: h.about,
	}
}

func (h *Handler) about(ctx context.Context, _ *api.Interaction, _ []api.InteractionOption, responder *interactions.Responder) error {
	embed, err := embeds.Text("DealFox", "Check regional Steam prices with `/price` and get DM sale alerts with `/track`. Track games and DLC with one-off or recurring rules, with an optional exact budget. Use `/track remove` to remove an individual rule.\n\nUse `/freegames subscribe` for free-to-keep PC game alerts from Steam, Epic, GOG, and Ubisoft. Select sources or choose `all`. Alerts go to your DMs unless someone with Manage Server selects a server channel. Offers are checked on US storefronts. Use `/freegames status` to view your subscriptions and `/freegames unsubscribe` to stop alerts.\n\nUse `/account delete` to delete your personal DealFox data. You'll need to confirm by replying `I agree` in a DM within 30 seconds.")
	if err != nil {
		return err
	}

	_, err = responder.Message(ctx, interactions.ResponseEmbed(embed, interactions.NoMentions()))

	return err
}
