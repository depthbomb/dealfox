package commands

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	tomogocommands "github.com/depthbomb/tomogo/commands"
	"github.com/depthbomb/tomogo/rest"
	"github.com/depthbomb/tomogo/testkit"
)

func TestCommandReferencesUsePublishedMentions(t *testing.T) {
	h, app := newTestHandler(t, &tracker.Service{Config: testutil.Config(t)})
	definitions, err := h.Definitions()
	if err != nil {
		t.Fatal(err)
	}
	for i := range definitions {
		definitions[i].ID = api.ID(100 + i)
		definitions[i].ApplicationID = 999
	}
	h.Published, err = tomogocommands.NewCatalog(definitions)
	if err != nil {
		t.Fatal(err)
	}
	about := dispatch(t, app, "about")
	text := *about.InitialResponses()[0].Response.Data.Embeds[0].Description
	for _, want := range []string{
		"</price:100>", "</track:101>", "</track remove:101>",
		"</freegames subscribe:102>", "</freegames status:102>", "</freegames unsubscribe:102>",
		"</account delete:103>",
	} {
		if !strings.Contains(text, want) || strings.Contains(text, "`"+want) {
			t.Errorf("about did not render clickable mention %s: %s", want, text)
		}
	}
	response := dispatch(t, app, "track", testkit.Subcommand("remove", testkit.StringOption("id", "invalid")))
	text = *(*response.OriginalEdits()[0].Embeds)[0].Description
	if text != "Use a rule ID from </track list:101>." {
		t.Fatalf("rule validation message = %q", text)
	}
	failure := h.deletionDMError(&rest.Error{StatusCode: http.StatusForbidden})
	text, public := commandErrorMessage(failure)
	if !public || !strings.Contains(text, "run </account delete:103> again") {
		t.Fatalf("DM failure message = %q, public=%t", text, public)
	}
	underlying := errors.New("connection failed")
	if h.deletionDMError(underlying) != underlying {
		t.Fatal("unexpected DM failure was masked")
	}
}

func TestMissingPublishedCommandsKeepReadableFallback(t *testing.T) {
	h := &Handler{}
	if got := h.mention("track", "list"); got != "`/track list`" {
		t.Fatalf("missing catalog fallback = %q", got)
	}
	var err error
	h.Published, err = tomogocommands.NewCatalog([]api.ApplicationCommand{{
		ID: 101, ApplicationID: 999, Name: "track",
	}})
	if err != nil {
		t.Fatal(err)
	}

	if got := h.mention("track", "list"); got != "`/track list`" {
		t.Fatalf("unpublished subcommand fallback = %q", got)
	}
}
