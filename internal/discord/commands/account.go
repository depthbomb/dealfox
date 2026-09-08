package commands

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	cuid "github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/discord/embeds"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/collector"
	"github.com/depthbomb/tomogo/events"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/preconditions"
	"github.com/depthbomb/tomogo/rest"
	"github.com/tomogo-framework/snowflake"
)

type pendingDeletions struct {
	mu    sync.Mutex
	users map[string]bool
}

type deletionConfirmation struct {
	mu       sync.Mutex
	user     api.ID
	channel  api.ID
	prompt   api.ID
	deadline time.Time
}

const deletionConfirmationTimeout = 30 * time.Second

func (p *pendingDeletions) begin(user string) (func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.users[user] {
		return nil, domain.Invalid("You already have an account deletion in progress. Check your DMs.")
	}

	// Leave Gateway callback capacity available to receive confirmation replies.
	if len(p.users) >= 4 {
		return nil, domain.Invalid("Account deletion is busy. Please try again shortly.")
	}

	if p.users == nil {
		p.users = make(map[string]bool)
	}
	p.users[user] = true

	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		delete(p.users, user)
	}, nil
}

func (c *deletionConfirmation) arm(prompt api.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompt = prompt
	c.deadline = time.Now().Add(deletionConfirmationTimeout)
}

func (c *deletionConfirmation) accepts(event events.MessageCreate) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := event.Data
	if m == nil || m.Author == nil {
		return false
	}

	return c.prompt != 0 && time.Now().Before(c.deadline) && m.ID > c.prompt && m.ChannelID == c.channel && m.Author.ID == c.user && !m.Author.Bot && m.WebhookID == 0 && m.Content == "I agree"
}

func (h *Handler) accountAvailable() preconditions.Check {
	return preconditions.Func(func(_ context.Context, i *api.Interaction) error {
		user, err := owner(i)
		if err != nil {
			return err
		}
		h.deletions.mu.Lock()
		pending := h.deletions.users[user]
		h.deletions.mu.Unlock()
		if pending {
			return &preconditions.Failure{
				Reason: "You have an account deletion in progress. Reply in your DMs or wait for it to expire before using another command.",
			}
		}

		return nil
	})
}

func (h *Handler) accountCommand() command {
	return command{
		definition: everywhere(api.ApplicationCommand{
			Name:        "account",
			Description: "Manage your DealFox data",
		}),
		preconditions: []preconditions.Check{h.rateLimit("account", 2, time.Minute)},
		subcommands: []subcommand{
			{
				definition: api.CommandOption{
					Name:        "delete",
					Description: "Delete your personal data after confirming by DM",
				},
				handle: h.account,
			},
		},
	}
}

func (h *Handler) account(ctx context.Context, i *api.Interaction, _ []api.InteractionOption, responder *interactions.Responder) error {
	if err := responder.DeferEphemeral(ctx); err != nil {
		return err
	}

	user, err := owner(i)
	if err != nil {
		return err
	}
	finish, err := h.deletions.begin(user)
	if err != nil {
		return err
	}
	defer finish()

	userID, err := snowflake.Parse(user)
	if err != nil {
		return err
	}
	// Open separately so the confirmation collector is registered before sending.
	channel, _, err := h.REST.Users().CreateDM(ctx, userID)
	if err != nil {
		return deletionDMError(err)
	}

	confirmation := &deletionConfirmation{
		user:    userID,
		channel: channel.ID,
	}
	stream, err := collector.Events(ctx, h.events, events.MessageCreateEvent(), collector.Options[events.MessageCreate]{
		Capacity: 1,
		MaxItems: 1,
		Timeout:  time.Minute,
		Filter:   confirmation.accepts,
	})
	if err != nil {
		return err
	}
	defer stream.Stop(nil)

	if err := accountResponse(ctx, responder, "Check your DMs", "I've opened a DM confirmation. Reply there with exactly `I agree` within 30 seconds of the prompt to delete your data."); err != nil {
		return err
	}

	embed, err := embeds.Text("Delete your data?", "This permanently removes your tracking rules, personal free-game subscriptions, and notification records. Server alerts stay active, with your identity removed from their configuration. This cannot be undone.\n\nReply here with exactly `I agree` within **30 seconds** to confirm. Otherwise, nothing will be deleted.")
	if err != nil {
		return err
	}
	prompt, _, err := h.REST.Messages().Create(ctx, channel.ID, api.MessageCreate{
		Embeds: []api.Embed{embed},
		AllowedMentions: &api.AllowedMentions{
			Parse: []string{},
		},
		Nonce:        cuid.Generate(),
		EnforceNonce: true,
	}, nil)
	if err != nil {
		return deletionDMError(err)
	}
	confirmation.arm(prompt.ID)
	waitCtx, cancel := context.WithTimeout(ctx, deletionConfirmationTimeout)
	_, waitErr := stream.Next(waitCtx)
	cancel()
	stream.Stop(nil)

	if waitErr != nil {
		if errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, collector.ErrTimeout) || errors.Is(waitErr, context.Canceled) {
			return h.accountResult(ctx, responder, channel.ID, prompt.ID, "Deletion cancelled", "Confirmation expired or was interrupted. Your data has not been deleted.")
		}

		return waitErr
	}

	if err := h.Tracker.DeleteAccount(ctx, user); err != nil {
		return err
	}

	return h.accountResult(ctx, responder, channel.ID, prompt.ID, "Data deleted", "Your tracking rules, personal subscriptions, and notification records have been deleted. You can use my commands again to start fresh.")
}

func accountResponse(ctx context.Context, responder *interactions.Responder, title, text string) error {
	embed, err := embeds.Text(title, text)
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

func deletionDMError(err error) error {
	if remote, ok := errors.AsType[*rest.Error](err); ok && remote.StatusCode == 403 {
		return domain.Invalid("I couldn't send the confirmation DM. Allow DMs from me, then run /account delete again. Nothing was deleted.")
	}

	return err
}

func (h *Handler) accountResult(ctx context.Context, responder *interactions.Responder, channel, prompt api.ID, title, text string) error {
	embed, err := embeds.Text(title, text)
	if err != nil {
		return err
	}
	responseCtx, cancel := responseContext(ctx)
	defer cancel()

	_, _, editErr := h.REST.Messages().Edit(responseCtx, channel, prompt, interactions.EditMessage(interactions.ReplaceEmbeds(embed)), nil)
	if editErr != nil {
		h.Logger.Warn("could not update account confirmation message", "error", editErr)
	}

	if err := accountResponse(ctx, responder, title, text); err != nil {
		return fmt.Errorf("account outcome: %s: %w", title, err)
	}

	return nil
}
