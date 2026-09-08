package commands

import (
	"context"
	"time"

	"github.com/depthbomb/tomogo/api"
	tomogocommands "github.com/depthbomb/tomogo/commands"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/registration"
)

func (h *Handler) observeCommand(name string, handler tomogocommands.Handler) tomogocommands.Handler {
	return func(ctx context.Context, interaction *api.Interaction, responder *interactions.Responder) (err error) {
		if h.Diagnostics == nil {
			return handler(ctx, interaction, responder)
		}
		operation := name
		data, ok := interaction.CommandData()
		if ok && len(data.Options) > 0 && data.Options[0].Type == api.OptionSubcommand {
			operation += "." + data.Options[0].Name
		}
		start := time.Now()
		returned := false
		defer func() {
			failure := err
			if !returned {
				failure = &registration.PanicError{}
			}
			h.Diagnostics.Observe("command", operation, time.Since(start), failure)
		}()
		err = handler(ctx, interaction, responder)
		returned = true

		return err
	}
}
