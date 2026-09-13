package commands

import (
	"context"
	"fmt"
	"strings"

	cuid "github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/discord/embeds"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	tomogocommands "github.com/depthbomb/tomogo/commands"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/preconditions"
)

func (h *Handler) trackCommand() command {
	return command{
		definition: everywhere(api.ApplicationCommand{
			Name:        "track",
			Description: "Manage Steam sale alerts I send by DM",
		}),
		subcommands: []subcommand{
			{
				definition: api.CommandOption{
					Name:        "add",
					Description: "Track a Steam game or DLC for a sale",
					Options: []api.CommandOption{
						gameOption(),
						{
							Type:        api.OptionString,
							Name:        "condition",
							Description: "When to notify",
							Required:    true,
							Choices: []api.CommandChoice{
								tomogocommands.StringChoice("Any sale", domain.AnySale),
								tomogocommands.StringChoice("Sale at or under a budget", domain.UnderBudget),
							},
						},
						{
							Type:        api.OptionString,
							Name:        "budget",
							Description: "Exact budget and currency, for example 19.99 USD",
							MaxLength:   new(32),
						},
						{
							Type:        api.OptionBoolean,
							Name:        "recurring",
							Description: "Notify after each new sale; defaults to one-off",
						},
						countryOption(h.Tracker.Config.DefaultCountry),
					},
				},
				preconditions: []preconditions.Check{
					h.rateLimit("add", h.Tracker.Config.TrackAddRateLimit, h.Tracker.Config.TrackAddRateWindow),
				},
				handle: h.trackAction(h.addTrack),
			},
			{
				definition: api.CommandOption{
					Name:        "list",
					Description: "List your active tracking rules",
				},
				handle: h.trackAction(func(ctx context.Context, user string, call commandCall) error {
					return h.listTracks(ctx, user, call.Responder)
				}),
			},
			{
				definition: api.CommandOption{
					Name:        "remove",
					Description: "Remove one tracking rule",
					Options: []api.CommandOption{
						{
							Type:        api.OptionString,
							Name:        "id",
							Description: "Rule ID shown by /track list",
							Required:    true,
							MinLength:   new(24),
							MaxLength:   new(24),
						},
					},
				},
				handle: h.trackAction(h.removeTrack),
			},
		},
	}
}

func (h *Handler) trackAction(action func(context.Context, string, commandCall) error) commandHandler {
	return func(ctx context.Context, call commandCall) error {
		if err := call.Responder.DeferEphemeral(ctx); err != nil {
			return err
		}

		user, err := owner(call.Interaction)
		if err != nil {
			return err
		}

		return action(ctx, user, call)
	}
}

func (h *Handler) addTrack(ctx context.Context, user string, call commandCall) error {
	cfg := h.Tracker.Config
	if !cfg.NewTracksEnabled {
		return domain.Invalid("New tracking rules are temporarily paused for maintenance.")
	}

	game, err := call.Arguments.String("game")
	if err != nil {
		return err
	}
	country, err := call.Arguments.StringOr("country", cfg.DefaultCountry)
	if err != nil {
		return err
	}
	condition, err := call.Arguments.String("condition")
	if err != nil {
		return err
	}
	budget, err := call.Arguments.StringOr("budget", "")
	if err != nil {
		return err
	}
	recurring, err := call.Arguments.BooleanOr("recurring", false)
	if err != nil {
		return err
	}
	r, p, err := h.Tracker.Add(ctx, tracker.AddRequest{
		OwnerID:   user,
		RequestID: call.Interaction.ID.String(),
		Game:      game,
		Country:   country,
		Condition: condition,
		Budget:    budget,
		Recurring: recurring,
	})
	if err != nil {
		return err
	}

	text := "I've queued your one-off notification and completed the rule.\n\n"
	if r.Enabled {
		cadence := "one-off"
		if r.Recurring {
			cadence = "recurring"
		}

		text = fmt.Sprintf("I'm tracking this item with a %s alert. Rule ID: `%s`.\n\n", cadence, r.ID)
	}

	embed, err := embeds.Price(p, text)
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

func (h *Handler) listTracks(ctx context.Context, user string, responder *interactions.Responder) error {
	rules, err := h.Tracker.Store.List(ctx, user)
	if err != nil {
		return err
	}

	if len(rules) == 0 {
		embed, err := embeds.Text("Tracking rules", "You have no active tracking rules.")
		if err != nil {
			return err
		}

		responseCtx, cancel := responseContext(ctx)
		defer cancel()
		_, err = responder.EditOriginalMessage(responseCtx, interactions.ReplaceEmbeds(embed), interactions.ReplaceAllowedMentions(api.AllowedMentions{
			Parse: []string{},
		}))

		return err
	}

	var pages []api.Embed
	var body strings.Builder
	flush := func() error {
		embed, err := embeds.Text("Your active tracking rules", body.String())
		if err != nil {
			return err
		}
		pages = append(pages, embed)
		body.Reset()

		return nil
	}
	for _, r := range rules {
		t, err := r.Target.Get()
		if err != nil {
			return err
		}

		app, err := t.App.Get()
		if err != nil {
			return err
		}
		condition := "any sale"
		if r.BudgetMinor != nil {
			condition = "sale at or under " + (domain.Money{
				Minor:    *r.BudgetMinor,
				Currency: r.BudgetCurrency,
			}).String()
		}

		cadence := "one-off"
		if r.Recurring {
			cadence = "recurring"
		}

		line := fmt.Sprintf("**%s** (%d, %s): %s, %s\n`%s`\n\n", embeds.EscapeMarkdown(embeds.Limit(app.Name, 150)), t.AppID, t.Country, condition, cadence, r.ID)
		if body.Len()+len(line) > 3500 {
			if err := flush(); err != nil {
				return err
			}
		}
		body.WriteString(line)
	}

	if err := flush(); err != nil {
		return err
	}

	responseCtx, cancel := responseContext(ctx)
	defer cancel()
	for index, embed := range pages {
		if index == 0 {
			_, err = responder.EditOriginalMessage(responseCtx, interactions.ReplaceEmbeds(embed), interactions.ReplaceAllowedMentions(api.AllowedMentions{
				Parse: []string{},
			}))
		} else {
			_, err = responder.Followup(responseCtx, interactions.FollowupEmbed(embed, interactions.Ephemeral(), interactions.NoMentions()))
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func (h *Handler) removeTrack(ctx context.Context, user string, call commandCall) error {
	id, err := call.Arguments.String("id")
	if err != nil || !cuid.IsValidLength(id, 24) {
		return domain.Invalid("Use a rule ID from " + h.mention("track", "list") + ".")
	}

	if err := h.Tracker.Remove(ctx, user, id); err != nil {
		return err
	}

	embed, err := embeds.Text("Rule removed", "I've removed that tracking rule.")
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
