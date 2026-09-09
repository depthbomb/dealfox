package discord

import (
	"context"
	"fmt"
	"time"

	"github.com/depthbomb/dealfox/internal/discord/embeds"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/rest"
	"github.com/tomogo-framework/snowflake"
)

type Sender struct {
	REST  *rest.Client
	DM    *rest.DMSender
	Steam ArtworkSource
}

type ArtworkSource interface {
	Artwork(context.Context, int64, string) (string, error)
}

func (s Sender) Send(ctx context.Context, destination, id string, payload domain.Payload) (string, error) {
	user, err := snowflake.Parse(destination)
	if err != nil {
		return "", err
	}

	var capsuleURL string
	if s.Steam != nil {
		imageCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		image, err := s.Steam.Artwork(imageCtx, payload.AppID, payload.Country)
		cancel()

		if err == nil {
			capsuleURL = image
		}
	}

	regular := domain.Money{
		Minor:    payload.Regular,
		Currency: payload.Currency,
	}
	current := domain.Money{
		Minor:    payload.Final,
		Currency: payload.Currency,
	}
	cadence := "I've completed this one-off alert."

	if payload.Recurring {
		cadence = "I'll rearm this recurring alert after the item goes off sale."
	}

	description := fmt.Sprintf("%s → %s (%d%% off) in %s\n\n%s", regular, current, payload.Discount, payload.Country, cadence)
	embed, err := embeds.Build(embeds.Game(payload.AppID, capsuleURL).SetTitle(embeds.Limit(payload.Name+" is on sale on Steam", 256)).SetDescription(description).SetFooter("Rule: " + payload.RuleID))
	if err != nil {
		return "", err
	}

	message, _, err := s.DM.Send(ctx, user, api.MessageCreate{
		Embeds: []api.Embed{embed},
		AllowedMentions: &api.AllowedMentions{
			Parse: []string{},
		},
		Nonce:        id,
		EnforceNonce: true,
	}, nil)
	if err != nil {
		return "", err
	}

	return message.ID.String(), nil
}
