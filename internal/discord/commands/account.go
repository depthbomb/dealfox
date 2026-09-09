package commands

import (
	"context"
	"errors"
	"sync"
	"time"

	cuid "github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/discord/embeds"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/collector"
	"github.com/depthbomb/tomogo/continuation"
	"github.com/depthbomb/tomogo/events"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/preconditions"
	"github.com/depthbomb/tomogo/registration"
	"github.com/depthbomb/tomogo/rest"
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
	if event.Data == nil || event.Data.Author == nil {
		return false
	}
	m := event.Data.Snapshot()

	return c.prompt != 0 && time.Now().Before(c.deadline) && m.ID > c.prompt && m.ChannelID == c.channel && m.Author.ID == c.user && !m.Author.Bot && m.WebhookID == 0 && m.Content == "I agree"
}

func (c *deletionConfirmation) candidate(event events.MessageCreate) bool {
	if event.Data == nil || event.Data.Author == nil {
		return false
	}
	m := event.Data.Snapshot()

	return m.ChannelID == c.channel && m.Author.ID == c.user && !m.Author.Bot && m.WebhookID == 0 && m.Content == "I agree"
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

func (h *Handler) account(ctx context.Context, call commandCall) error {
	user, err := owner(call.Interaction)
	if err != nil {
		return err
	}
	finish, err := h.deletions.begin(user)
	if err != nil {
		return err
	}
	transferred := false
	defer func() {
		if !transferred {
			finish()
		}
	}()
	ticket, err := h.continuations.Reserve(ctx, continuation.Options{
		Timeout: h.Tracker.Config.CommandTimeout,
		Identity: registration.Identity{
			Feature: "account",
			Handler: "delete",
		},
	})
	if errors.Is(err, continuation.ErrFull) {
		return domain.Invalid("Account deletion is busy. Please try again shortly.")
	}

	if err != nil {
		return err
	}
	defer ticket.Rollback()
	if err := call.Responder.DeferEphemeral(ctx); err != nil {
		return err
	}
	actor, _ := call.Interaction.Actor()
	userID := actor.ID
	if err := ticket.Start(call.Responder, func(ctx context.Context, owned *interactions.Continuation) (err error) {
		defer finish()
		started := time.Now()
		returned := false
		defer func() {
			failure := err
			if !returned {
				failure = &registration.PanicError{}
			}
			h.Diagnostics.Observe("continuation", "account.delete", time.Since(started), failure)
		}()
		err = h.confirmAccount(ctx, userID, owned)
		returned = true

		return err
	}); err != nil {
		return err
	}
	transferred = true

	return nil
}

func (h *Handler) confirmAccount(ctx context.Context, user api.ID, owned *interactions.Continuation) (result error) {
	var channel, prompt api.ID
	title := "Deletion cancelled"
	text := "Confirmation expired or was interrupted. Your data has not been deleted."
	defer func() {
		// Close known prompts and complete the deferred response within one cleanup
		// budget, including during application shutdown. Never retry an uncertain edit.
		cleanup, cancel := responseContext(ctx)
		defer cancel()
		if result != nil && !errors.Is(result, context.Canceled) && !errors.Is(result, context.DeadlineExceeded) {
			title = "Request unsuccessful"
			message, public := commandErrorMessage(result)
			if !public {
				reference := cuid.Generate()
				h.Diagnostics.Reference("account", reference, result)
				h.Logger.Error("account continuation failed", "reference", reference, "error", result)
				message = "I couldn't complete that request. Please try again. Reference: `" + reference + "`."
			}
			text = message
		}
		edit, err := accountEdit(title, text)
		if err != nil {
			result = errors.Join(result, err)

			return
		}

		if prompt != 0 {
			_, _, err = h.REST.Messages().Edit(cleanup, channel, prompt, edit, nil)
			result = errors.Join(result, err)
		}

		if owned.State() != interactions.ResponseUncertain {
			_, err = owned.EditOriginal(cleanup, edit)
			result = errors.Join(result, err)
		}
	}()
	var err error
	if h.DM != nil {
		channel, _, err = h.DM.Open(ctx, user)
	} else {
		var opened api.Channel
		opened, _, err = h.REST.Users().CreateDM(ctx, user)
		channel = opened.ID
	}
	if err != nil {
		return h.deletionDMError(err)
	}
	confirmation := &deletionConfirmation{
		user:    user,
		channel: channel,
	}
	// Keep candidates that arrive before the prompt POST returns. Validate the
	// returned prompt ID and deadline before accepting any candidate as consent.
	stream, err := collector.Events(ctx, h.events, events.MessageCreateEvent(), collector.Options[events.MessageCreate]{
		Capacity: 16,
		Timeout:  h.Tracker.Config.CommandTimeout,
		Filter:   confirmation.candidate,
	})
	if err != nil {
		return err
	}
	defer stream.Stop(nil)
	embed, err := embeds.Text("Delete your data?", "This permanently removes your tracking rules, personal free-game subscriptions, and notification records. Server alerts stay active, with your identity removed from their configuration. This cannot be undone.\n\nReply here with exactly `I agree` within **30 seconds** to confirm. Otherwise, nothing will be deleted.")
	if err != nil {
		return err
	}
	sent, _, err := h.REST.Messages().Create(ctx, channel, api.MessageCreate{
		Embeds: []api.Embed{embed},
		AllowedMentions: &api.AllowedMentions{
			Parse: []string{},
		},
		Nonce:        cuid.Generate(),
		EnforceNonce: true,
	}, nil)
	if err != nil {
		return h.deletionDMError(err)
	}

	if sent.ID == 0 || sent.ChannelID != channel {
		return errors.New("confirmation prompt identity is unavailable")
	}
	prompt = sent.ID
	confirmation.arm(prompt)
	waitCtx, cancel := context.WithTimeout(ctx, deletionConfirmationTimeout)
	defer cancel()
	edit, err := accountEdit("Check your DMs", "I've opened a DM confirmation. Reply there with exactly `I agree` within 30 seconds of the prompt to delete your data.")
	if err != nil {
		return err
	}

	if _, err := owned.EditOriginal(waitCtx, edit); err != nil {
		return err
	}
	for {
		reply, err := stream.Next(waitCtx)
		if err != nil {
			return err
		}

		if err := stream.Err(); err != nil {
			return err
		}

		if confirmation.accepts(reply) {
			break
		}
	}
	stream.Stop(nil)
	if err := waitCtx.Err(); err != nil {
		return err
	}

	if err := h.Tracker.DeleteAccount(ctx, user.String()); err != nil {
		return err
	}
	title = "Data deleted"
	text = "Your tracking rules, personal subscriptions, and notification records have been deleted. You can use my commands again to start fresh."

	return nil
}

func accountEdit(title, text string) (api.MessageEdit, error) {
	embed, err := embeds.Text(title, text)
	if err != nil {
		return api.MessageEdit{}, err
	}

	return interactions.EditMessage(interactions.ReplaceEmbeds(embed), interactions.ReplaceAllowedMentions(api.AllowedMentions{
		Parse: []string{},
	})), nil
}

func (h *Handler) deletionDMError(err error) error {
	if remote, ok := errors.AsType[*rest.Error](err); ok && remote.StatusCode == 403 {
		return domain.Invalid("I couldn't send the confirmation DM. Allow DMs from me, then run " + h.mention("account", "delete") + " again. Nothing was deleted.")
	}

	return err
}
