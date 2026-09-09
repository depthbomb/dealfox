package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/depthbomb/dealfox/internal/discord/embeds"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/permissions"
	"github.com/depthbomb/tomogo/preconditions"
	"github.com/depthbomb/tomogo/resource"
)

func freeSourcesOption(required bool) api.CommandOption {
	return api.CommandOption{
		Type:        api.OptionString,
		Name:        "sources",
		Description: "steam, epic, gog, ubisoft, comma-separated choices, or all",
		Required:    required,
		MaxLength:   new(100),
	}
}

func serverOption() api.CommandOption {
	return api.CommandOption{
		Type:        api.OptionBoolean,
		Name:        "server",
		Description: "Manage this server's channel alerts (requires Manage Server); defaults to your DMs",
	}
}

func manageServer(i *api.Interaction) bool {
	return i.GuildID != 0 && i.Member != nil && (i.Member.Permissions.Has(api.PermissionManageGuild) || i.Member.Permissions.Has(api.PermissionAdministrator))
}

func freeScope(i *api.Interaction, user string, server bool) (string, error) {
	if server {
		if !manageServer(i) {
			return "", domain.Invalid("You need Manage Server in this server to manage its channel alerts. Omit the channel/server option to manage your own DM alerts.")
		}

		return "guild:" + i.GuildID.String(), nil
	}

	return "dm:" + user, nil
}

func (h *Handler) freeGamesCommand() command {
	return command{
		definition: everywhere(api.ApplicationCommand{
			Name:        "freegames",
			Description: "Free-to-keep PC game alerts from Steam, Epic, GOG, and Ubisoft (US offers)",
		}),
		subcommands: []subcommand{
			{
				definition: api.CommandOption{
					Name:        "subscribe",
					Description: "Add sources to your DM alerts or set this server's alert channel",
					Options: []api.CommandOption{
						freeSourcesOption(true),
						{
							Type:         api.OptionChannel,
							Name:         "channel",
							Description:  "Server alert channel (requires Manage Server); omit for DMs",
							ChannelTypes: []api.ChannelType{api.ChannelGuildText, api.ChannelGuildAnnouncement},
						},
					},
				},
				preconditions: []preconditions.Check{h.rateLimit("freegames", 10, time.Minute)},
				handle:        h.freeGamesAction("subscribe"),
			},
			{
				definition: api.CommandOption{
					Name:        "unsubscribe",
					Description: "Stop alerts from selected sources or all sources",
					Options:     []api.CommandOption{freeSourcesOption(true), serverOption()},
				},
				preconditions: []preconditions.Check{h.rateLimit("freegames", 10, time.Minute)},
				handle:        h.freeGamesAction("unsubscribe"),
			},
			{
				definition: api.CommandOption{
					Name:        "status",
					Description: "View your sources and monitor health",
					Options:     []api.CommandOption{serverOption()},
				},
				handle: h.freeGamesAction("status"),
			},
		},
	}
}

func (h *Handler) freeGamesAction(action string) commandHandler {
	return func(ctx context.Context, call commandCall) error {
		return h.freeGames(ctx, call, action)
	}
}

func (h *Handler) verifyFreeChannel(ctx context.Context, i *api.Interaction, channelID api.ID) error {
	if h.REST == nil {
		return domain.Invalid("Channel validation is unavailable. Please try again later.")
	}

	bot, _, err := h.REST.Users().GetCurrent(ctx)
	if err != nil {
		return err
	}
	if bot.ID == 0 {
		return domain.Invalid("Channel validation is unavailable. Please try again later.")
	}
	resources, err := resource.New(h.REST, resource.Config{})
	if err != nil {
		return err
	}
	result, err := resources.ChannelInGuild(i.GuildID, channelID).FetchPermissionsFor(ctx, bot.ID)
	if result.Channel.Value == nil {
		if errors.Is(err, permissions.ErrInvalid) {
			return domain.Invalid("Choose a text or announcement channel in this server.")
		}

		return domain.Invalid("I couldn't access that channel. Make sure I'm installed in this server and can view it.")
	}
	channel := result.Channel.Value.Snapshot()
	if channel.GuildID != i.GuildID || (channel.Type != api.ChannelGuildText && channel.Type != api.ChannelGuildAnnouncement) {
		return domain.Invalid("Choose a text or announcement channel in this server.")
	}

	if err != nil && !errors.Is(err, permissions.ErrIncomplete) && !errors.Is(err, permissions.ErrInvalid) {
		return err
	}

	if err != nil || !result.Permissions.Has(api.PermissionViewChannel|api.PermissionSendMessages|api.PermissionEmbedLinks) {
		return domain.Invalid("I need View Channel, Send Messages, and Embed Links in that channel.")
	}

	return nil
}

func (h *Handler) freeGames(ctx context.Context, call commandCall, action string) error {
	i, responder := call.Interaction, call.Responder
	if err := responder.DeferEphemeral(ctx); err != nil {
		return err
	}

	user, err := owner(i)
	if err != nil {
		return err
	}

	release, err := h.Tracker.LockAccount(ctx, user)
	if err != nil {
		return err
	}
	defer release()

	channel, hasChannel, err := call.Arguments.OptionalSnowflake("channel")
	if err != nil {
		return domain.Invalid("Choose a channel in this server.")
	}
	server, err := call.Arguments.BooleanOr("server", false)
	if err != nil {
		return err
	}
	server = hasChannel || server
	scope, err := freeScope(i, user, server)
	if err != nil {
		return err
	}

	if action == "status" {
		return h.freeGamesStatus(ctx, scope, responder)
	}

	sourceNames, err := call.Arguments.String("sources")
	if err != nil {
		return err
	}
	sources, err := freegames.ParseSources(sourceNames)
	if err != nil {
		return err
	}

	enabled := action == "subscribe"
	destination := user
	if enabled && server {
		if channel == 0 {
			return domain.Invalid("Choose a channel in this server.")
		}

		if err := h.verifyFreeChannel(ctx, i, channel); err != nil {
			return err
		}
		destination = channel.String()
	}

	if err := h.Tracker.Store.SetFreeSubscriptions(ctx, scope, destination, user, sources, enabled); err != nil {
		return err
	}

	return h.freeGamesStatus(ctx, scope, responder)
}

func (h *Handler) freeGamesStatus(ctx context.Context, scope string, responder *interactions.Responder) error {
	subs, err := h.Tracker.Store.FreeSubscriptions(ctx, scope)
	if err != nil {
		return err
	}

	var body strings.Builder
	body.WriteString("**Free-to-keep PC games · US storefronts**\n")

	if len(subs) == 0 {
		body.WriteString("No sources subscribed.\n")
	}

	for _, sub := range subs {
		destination := "your DMs"
		if sub.DestinationKind == "channel" {
			destination = "<#" + sub.DestinationID + ">"
		}

		fmt.Fprintf(&body, "• %s → %s\n", sub.Source, destination)
	}
	body.WriteString("\nNew subscriptions also receive currently verified offers. Subscribing adds sources; unsubscribe removes them. A server uses one alert channel.\n\n")

	monitors, err := h.Tracker.Store.FreeMonitors(ctx)
	if err != nil {
		return err
	}
	for _, monitor := range monitors {
		checked := "not checked yet"
		if monitor.CheckedAt != nil {
			checked = fmt.Sprintf("checked <t:%d:R>", monitor.CheckedAt.Unix())
		}

		fmt.Fprintf(&body, "**%s:** %s, %s\n", monitor.ID, monitor.Status, checked)
	}
	if !h.Tracker.Config.FreeGamesEnabled || !h.Tracker.Config.DeliveryEnabled {
		body.WriteString("\nMonitoring or delivery is paused for maintenance.\n")
	}

	body.WriteString("\nRegional availability can vary. Unconfirmed offers are held for review.")

	embed, err := embeds.Text("Free game alerts", body.String())
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
