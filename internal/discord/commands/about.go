package commands

import (
	"context"
	"fmt"

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

func (h *Handler) about(ctx context.Context, call commandCall) error {
	embed, err := embeds.Text("DealFox", fmt.Sprintf("Check regional Steam prices with %s and get DM sale alerts with %s. Track games and DLC with one-off or recurring rules, with an optional exact budget. Use %s to remove an individual rule.\n\nUse %s for free-to-keep PC game alerts from Steam, Epic, GOG, and Ubisoft. Select sources or choose `all`. Alerts go to your DMs unless someone with Manage Server selects a server channel. Offers are checked on US storefronts. Use %s to view your subscriptions and %s to stop alerts.\n\nUse %s to delete your personal DealFox data. You'll need to confirm by replying `I agree` in a DM within 30 seconds.",
		h.mention("price"), h.mention("track"), h.mention("track", "remove"),
		h.mention("freegames", "subscribe"), h.mention("freegames", "status"), h.mention("freegames", "unsubscribe"),
		h.mention("account", "delete")))
	if err != nil {
		return err
	}

	_, err = call.Responder.Message(ctx, interactions.ResponseEmbed(embed))

	return err
}
