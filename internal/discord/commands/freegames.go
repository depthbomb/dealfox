package commands

import (
	"context"
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
	"github.com/tomogo-framework/snowflake"
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
	return func(ctx context.Context, i *api.Interaction, options []api.InteractionOption, responder *interactions.Responder) error {
		return h.freeGames(ctx, i, options, responder, action)
	}
}

func (h *Handler) verifyFreeChannel(ctx context.Context, i *api.Interaction, channelID api.ID) error {
	if h.REST == nil {
		return domain.Invalid("Channel validation is unavailable. Please try again later.")
	}

	channel, _, err := h.REST.Channels().Get(ctx, channelID)
	if err != nil {
		return domain.Invalid("I couldn't access that channel. Make sure I'm installed in this server and can view it.")
	}

	if channel.GuildID != i.GuildID || (channel.Type != api.ChannelGuildText && channel.Type != api.ChannelGuildAnnouncement) {
		return domain.Invalid("Choose a text or announcement channel in this server.")
	}

	guild, _, err := h.REST.Guilds().Get(ctx, i.GuildID, false)
	if err != nil {
		return err
	}
	user, _, err := h.REST.Users().GetCurrent(ctx)
	if err != nil {
		return err
	}
	member, _, err := h.REST.Guilds().GetMember(ctx, i.GuildID, user.ID)
	if err != nil {
		return err
	}
	bits, err := permissions.Channel(permissions.ChannelInput{
		Guild:   guild,
		Member:  member,
		Channel: channel,
		Now:     time.Now(),
	})
	if err != nil || !bits.Has(api.PermissionViewChannel|api.PermissionSendMessages|api.PermissionEmbedLinks) {
		return domain.Invalid("I need View Channel, Send Messages, and Embed Links in that channel.")
	}

	return nil
}

func (h *Handler) freeGames(ctx context.Context, i *api.Interaction, options []api.InteractionOption, responder *interactions.Responder, action string) error {
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

	channel := stringOption(options, "channel", "")
	server := channel != "" || boolOption(options, "server")
	scope, err := freeScope(i, user, server)
	if err != nil {
		return err
	}

	if action == "status" {
		return h.freeGamesStatus(ctx, scope, responder)
	}

	sources, err := freegames.ParseSources(stringOption(options, "sources", ""))
	if err != nil {
		return err
	}

	enabled := action == "subscribe"
	destination := user
	if enabled && server {
		id, err := snowflake.Parse(channel)
		if err != nil || id == 0 {
			return domain.Invalid("Choose a channel in this server.")
		}

		if err := h.verifyFreeChannel(ctx, i, id); err != nil {
			return err
		}
		destination = channel
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
