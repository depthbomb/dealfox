package discord

import (
	"context"
	"fmt"

	"github.com/depthbomb/dealfox/internal/discord/embeds"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/tomogo/api"
	embedbuilder "github.com/tomogo-framework/embed-builder"
	"github.com/tomogo-framework/snowflake"
)

func freeGameEmbed(offer freegames.Offer) (api.Embed, error) {
	description := "**Free to claim and keep**\n\n" + embeds.EscapeMarkdown(embeds.Limit(offer.Description, 1500))
	if !offer.EndsAt.IsZero() {
		description += fmt.Sprintf("\n\nClaim before <t:%d:F> (<t:%d:R>).", offer.EndsAt.Unix(), offer.EndsAt.Unix())
	}

	if offer.ClaimURL != "" {
		description += "\n[Claim this giveaway](" + offer.ClaimURL + ")"
	}

	return embeds.Build(embedbuilder.New().SetTitle(embeds.Limit(offer.Title+" is free on "+offer.Source, 256)).SetDescription(description).SetURL(offer.URL).SetImage(offer.ImageURL).SetColor(0xF28C28).SetFooter(offer.Platform + " · US offer · Regional availability may vary"))
}

func (s Sender) SendFree(ctx context.Context, kind, destination, id string, offer freegames.Offer) (string, error) {
	destinationID, err := snowflake.Parse(destination)
	if err != nil {
		return "", err
	}

	embed, err := freeGameEmbed(offer)
	if err != nil {
		return "", err
	}

	payload := api.MessageCreate{
		Embeds: []api.Embed{embed},
		AllowedMentions: &api.AllowedMentions{
			Parse: []string{},
		},
		Nonce:        id,
		EnforceNonce: true,
	}
	var message api.Message
	switch kind {
	case "dm":
		message, _, err = s.REST.Users().SendDM(ctx, destinationID, payload, nil)
	case "channel":
		message, _, err = s.REST.Messages().Create(ctx, destinationID, payload, nil)
	default:
		return "", fmt.Errorf("unknown free game delivery kind %q", kind)
	}

	if err != nil {
		return "", err
	}

	return message.ID.String(), nil
}
