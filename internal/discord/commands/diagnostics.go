package commands

import (
	"context"
	"strings"

	tomogocommands "github.com/depthbomb/tomogo/commands"
)

func (h *Handler) observeCommand(_ context.Context, execution tomogocommands.Execution) {
	operation := strings.Join(append([]string{execution.Command}, execution.Path...), ".")
	h.Diagnostics.Observe("command", operation, execution.Duration, execution.Err)
}
