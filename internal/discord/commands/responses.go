package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	cuid "github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/discord/embeds"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/events"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/preconditions"
)

func responseContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

// commandErrorMessage only exposes a single failure. Event dispatch can join
// failures from multiple listeners, so finding a public error anywhere in the
// error tree must not hide another listener's unexpected error.
func commandErrorMessage(failure error) (string, bool) {
	switch failure := failure.(type) {
	case *domain.PublicError:
		if failure == nil {
			return "", false
		}

		return failure.Message, true
	case *preconditions.Failure:
		return failure.Error(), true
	case *preconditions.CooldownError:
		seconds := (failure.RetryAfter-1)/time.Second + 1
		unit := "seconds"
		if seconds == 1 {
			unit = "second"
		}

		return fmt.Sprintf("Please try again in %d %s.", seconds, unit), true
	case interface{ Unwrap() []error }:
		var single error
		for _, child := range failure.Unwrap() {
			if child == nil {
				continue
			}

			if single != nil {
				return "", false
			}
			single = child
		}

		return commandErrorMessage(single)
	case interface{ Unwrap() error }:
		return commandErrorMessage(failure.Unwrap())
	default:
		return "", false
	}
}

// HandleInteractionError reports command failures after framework dispatch and
// panic recovery. Successful responses remain owned by command handlers. A
// response-operation failure is returned for Hooks.Error without another send.
func (h *Handler) HandleInteractionError(ctx context.Context, event events.InteractionCreate, failure error) error {
	if failure == nil || event.Interaction == nil || event.Interaction.Type != api.InteractionApplicationCommand || event.Responder == nil {
		return failure
	}

	if _, ok := errors.AsType[*interactions.ResponseError](failure); ok {
		return failure
	}

	state := event.Responder.State()
	if state == interactions.ResponseUncertain || state == interactions.ResponseAcknowledging || state == interactions.ResponseResponding {
		return failure
	}

	message, public := commandErrorMessage(failure)
	if !public {
		message = "I couldn't complete that request. Please try again."
		name := ""
		if data, ok := event.Interaction.CommandData(); ok {
			name = data.Name
		}

		reference := cuid.Generate()
		h.Diagnostics.Reference(name, reference, failure)
		h.Logger.Error("command failed", "command", name, "reference", reference, "error", failure)
		message += " Reference: `" + reference + "`."
	}

	embed, err := embeds.Text("Request unsuccessful", message)
	if err != nil {
		return err
	}

	responseCtx, cancel := responseContext(ctx)
	defer cancel()

	return event.Responder.RespondError(responseCtx, interactions.ResponseEmbed(embed, interactions.Ephemeral(), interactions.NoMentions()))
}
