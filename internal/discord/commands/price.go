package commands

import (
	"context"

	"github.com/depthbomb/dealfox/internal/discord/embeds"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/preconditions"
)

func gameOption() api.CommandOption {
	return api.CommandOption{
		Type:        api.OptionString,
		Name:        "game",
		Description: "Steam game or DLC app ID, store URL, or exact name",
		Required:    true,
		MaxLength:   new(300),
	}
}

func countryOption(country string) api.CommandOption {
	return api.CommandOption{
		Type:        api.OptionString,
		Name:        "country",
		Description: "Two-letter Steam country (default " + country + ")",
		MinLength:   new(2),
		MaxLength:   new(2),
	}
}

func (h *Handler) priceCommand() command {
	return command{
		definition: everywhere(api.ApplicationCommand{
			Name:        "price",
			Description: "Check the current Steam price for a game or DLC",
			Options:     []api.CommandOption{gameOption(), countryOption(h.Tracker.Config.DefaultCountry)},
		}),
		preconditions: []preconditions.Check{
			h.rateLimit("price", h.Tracker.Config.PriceRateLimit, h.Tracker.Config.PriceRateWindow),
		},
		handle: h.price,
	}
}

func (h *Handler) price(ctx context.Context, call commandCall) error {
	if err := call.Responder.DeferEphemeral(ctx); err != nil {
		return err
	}

	cfg := h.Tracker.Config
	if !cfg.PriceChecksEnabled {
		return domain.Invalid("Price checks are temporarily paused for maintenance.")
	}

	game, err := call.Arguments.String("game")
	if err != nil {
		return err
	}
	country, err := call.Arguments.StringOr("country", cfg.DefaultCountry)
	if err != nil {
		return err
	}
	p, err := h.Tracker.Price(ctx, game, country)
	if err != nil {
		return err
	}

	embed, err := embeds.Price(p, "")
	if err != nil {
		return err
	}

	responseCtx, cancel := responseContext(ctx)
	defer cancel()
	_, err = call.Responder.EditOriginalMessage(responseCtx, interactions.ReplaceEmbeds(embed), interactions.ReplaceAllowedMentions(api.AllowedMentions{
		Parse: []string{},
	}))

	return err
}
