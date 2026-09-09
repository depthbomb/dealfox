package commands

import "strings"

func (h *Handler) mention(path ...string) string {
	mention, err := h.Published.Mention(path...)
	if err == nil {
		return mention
	}

	// Keep help usable when a command has not been published yet or listing failed.
	return "`/" + strings.Join(path, " ") + "`"
}
