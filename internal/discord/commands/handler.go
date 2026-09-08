package commands

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/depthbomb/dealfox/internal/diagnostics"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo"
	"github.com/depthbomb/tomogo/api"
	tomogocommands "github.com/depthbomb/tomogo/commands"
	"github.com/depthbomb/tomogo/events"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/preconditions"
	"github.com/depthbomb/tomogo/rest"
)

type commandHandler func(context.Context, *api.Interaction, []api.InteractionOption, *interactions.Responder) error

type command struct {
	definition    api.ApplicationCommand
	preconditions []preconditions.Check
	subcommands   []subcommand
	handle        commandHandler
}

type subcommand struct {
	definition    api.CommandOption
	preconditions []preconditions.Check
	handle        commandHandler
}

// Handler owns command registration, execution, and the shared error policy.
type Handler struct {
	Tracker     *tracker.Service
	Logger      *slog.Logger
	Diagnostics *diagnostics.Recorder
	REST        *rest.Client
	limiter     limiter
	deletions   pendingDeletions
	events      *events.Registry
}

func owner(i *api.Interaction) (string, error) {
	user, ok := i.Actor()
	if ok && user.ID != 0 {
		return user.ID.String(), nil
	}

	return "", domain.Invalid("Discord did not provide the invoking user.")
}

func stringOption(options []api.InteractionOption, name, fallback string) string {
	for _, option := range options {
		if option.Name == name {
			value, ok := option.String()
			if ok {
				return value
			}
		}
	}

	return fallback
}

func boolOption(options []api.InteractionOption, name string) bool {
	for _, option := range options {
		if option.Name == name {
			value, ok := option.Boolean()

			return ok && value
		}
	}

	return false
}

func everywhere(def api.ApplicationCommand) api.ApplicationCommand {
	def.Type = api.CommandChatInput
	def.IntegrationTypes = []api.IntegrationType{api.IntegrationGuildInstall, api.IntegrationUserInstall}
	def.Contexts = []api.InteractionContextType{api.InteractionContextGuild, api.InteractionContextBotDM, api.InteractionContextPrivateChannel}

	return def
}

func invoke(handle commandHandler, timeout time.Duration) func(context.Context, tomogocommands.Call[tomogocommands.Input]) error {
	if handle == nil {
		return nil
	}

	return func(ctx context.Context, call tomogocommands.Call[tomogocommands.Input]) error {
		workCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		data, ok := call.Interaction.CommandData()
		if !ok {
			return fmt.Errorf("missing command data for /%s", call.Arguments.Command())
		}

		options := data.Options
		for range call.Arguments.Path() {
			options = options[0].Options
		}

		return handle(workCtx, call.Interaction, options, call.Responder)
	}
}

func (c command) bind(checks []preconditions.Check, timeout time.Duration) (api.ApplicationCommand, tomogocommands.Handler, error) {
	decode := func(input tomogocommands.Input) (tomogocommands.Input, error) {
		return input, nil
	}
	checks = append(append([]preconditions.Check{}, checks...), c.preconditions...)
	if len(c.subcommands) != 0 {
		tree := tomogocommands.CommandTree{
			Root:          c.definition,
			Preconditions: checks,
		}
		for _, sub := range c.subcommands {
			definition := sub.definition
			definition.Type = api.OptionSubcommand
			branch, err := tomogocommands.NewBranch(definition)
			if err != nil {
				return api.ApplicationCommand{}, nil, err
			}
			leaf, err := tomogocommands.NewExecutableSubcommand(branch, decode, invoke(sub.handle, timeout), sub.preconditions...)
			if err != nil {
				return api.ApplicationCommand{}, nil, err
			}
			tree.Branches = append(tree.Branches, leaf)
		}
		definition, err := tree.Definition()
		if err != nil {
			return api.ApplicationCommand{}, nil, err
		}
		handler, err := tree.CommandHandler()

		return definition, handler, err
	}

	binding, err := tomogocommands.NewBinding(c.definition, decode)
	if err != nil {
		return api.ApplicationCommand{}, nil, err
	}
	registered := tomogocommands.Command[tomogocommands.Input]{
		Binding:       binding,
		Preconditions: checks,
		Handler:       invoke(c.handle, timeout),
	}
	handler, err := registered.CommandHandler()

	return registered.Definition(), handler, err
}

func (h *Handler) commands() []command {
	return []command{h.priceCommand(), h.trackCommand(), h.freeGamesCommand(), h.accountCommand(), h.aboutCommand()}
}

// Definitions returns the commands used for both registration and publication.
func (h *Handler) Definitions() ([]api.ApplicationCommand, error) {
	var result []api.ApplicationCommand
	for _, c := range h.commands() {
		definition, _, err := c.bind(nil, h.Tracker.Config.CommandTimeout)
		if err != nil {
			return nil, fmt.Errorf("define /%s: %w", c.definition.Name, err)
		}
		result = append(result, definition)
	}

	return result, nil
}

// Register installs commands for Tomogo's default router without publishing.
func (h *Handler) Register(app *tomogo.App) error {
	h.events = app.Events()
	cfg := h.Tracker.Config
	sharedLimit := h.rateLimit("command", cfg.CommandRateLimit, cfg.CommandRateWindow)
	for _, c := range h.commands() {
		definition, handler, err := c.bind([]preconditions.Check{sharedLimit, h.accountAvailable()}, cfg.CommandTimeout)
		if err != nil {
			return fmt.Errorf("bind /%s: %w", c.definition.Name, err)
		}

		if err := app.Commands().Register(definition, h.observeCommand(definition.Name, handler)); err != nil {
			return fmt.Errorf("register /%s: %w", c.definition.Name, err)
		}
	}

	return nil
}
