package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	cuid "github.com/depthbomb/cuid2"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	tomogocommands "github.com/depthbomb/tomogo/commands"
	"github.com/depthbomb/tomogo/components"
	"github.com/depthbomb/tomogo/events"
	"github.com/depthbomb/tomogo/execution"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/preconditions"
	"github.com/depthbomb/tomogo/registration"
	"github.com/depthbomb/tomogo/testkit"
)

type failedAcknowledgement struct {
	interactions.Transport
	calls int
	err   error
}

type responseContextProbe struct {
	interactions.Transport
	t       *testing.T
	initial int
	edits   int
}

type failedFollowup struct {
	interactions.Transport
	calls int
	err   error
}

func assertNoMentions(t *testing.T, mentions *api.AllowedMentions) {
	t.Helper()
	if mentions == nil || mentions.Parse == nil || len(mentions.Parse) != 0 || len(mentions.Users) != 0 || len(mentions.Roles) != 0 || mentions.RepliedUser {
		t.Fatalf("response must explicitly disable all mentions: %+v", mentions)
	}
}

func (f *failedFollowup) CreateResponse(context.Context, api.ID, string, api.InteractionResponse, bool) (*api.InteractionCallbackResponse, error) {
	return nil, nil
}

func (f *failedFollowup) CreateFollowup(context.Context, api.ID, string, api.WebhookExecute) (*api.Message, error) {
	f.calls++

	return nil, f.err
}

func (p *responseContextProbe) CreateResponse(context.Context, api.ID, string, api.InteractionResponse, bool) (*api.InteractionCallbackResponse, error) {
	p.initial++

	return nil, nil
}

func (p *responseContextProbe) EditOriginal(ctx context.Context, _ api.ID, _ string, _ api.MessageEdit) (*api.Message, error) {
	p.edits++
	if ctx.Err() != nil {
		p.t.Fatal("error response inherited cancellation")
	}

	deadline, ok := ctx.Deadline()
	if remaining := time.Until(deadline); !ok || remaining <= 0 || remaining > 10*time.Second {
		p.t.Fatal("error response needs a bounded live context")
	}

	return &api.Message{}, nil
}

func (f *failedAcknowledgement) CreateResponse(context.Context, api.ID, string, api.InteractionResponse, bool) (*api.InteractionCallbackResponse, error) {
	f.calls++

	return nil, f.err
}

func TestHandlersChooseAcknowledgement(t *testing.T) {
	b, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
	})
	about := dispatch(t, app, "about")
	if about.InitialResponses()[0].Response.Type != api.CallbackChannelMessage {
		t.Fatal("about should reply immediately without central deferral")
	}

	b.Tracker.Config.PriceChecksEnabled = false
	price := dispatch(t, app, "price", testkit.StringOption("game", "400"))
	if price.InitialResponses()[0].Response.Type != api.CallbackDeferredChannelMessage || len(price.Followups()) != 0 {
		t.Fatal("price should defer and complete its original response")
	}
}

func TestHandlerErrorReportingLifecycle(t *testing.T) {
	for _, mode := range []string{"unacknowledged", "deferred public", "deferred ephemeral", "replied", "completed deferral"} {
		t.Run(mode, func(t *testing.T) {
			_, app := newTestHandler(t, &tracker.Service{
				Config: testutil.Config(t),
			})
			harness := testkit.NewInteractionHarness(testkit.CommandInteraction("test"))
			err := app.Commands().Register(api.ApplicationCommand{
				Name:        "test",
				Description: "Test response lifecycle",
			}, func(ctx context.Context, _ *api.Interaction, responder *interactions.Responder) error {
				switch mode {
				case "deferred public", "deferred ephemeral", "completed deferral":
					if err := responder.Defer(ctx, mode != "deferred public"); err != nil {
						return err
					}

					if mode == "completed deferral" {
						if _, err := responder.EditOriginal(ctx, interactions.Edit("Already completed")); err != nil {
							return err
						}
					}
				case "replied":
					if _, err := responder.Message(ctx, interactions.Response("Already completed")); err != nil {
						return err
					}
				}

				return domain.Invalid("Invalid selection")
			})
			if err != nil {
				t.Fatal(err)
			}

			if err := app.DispatchInteraction(t.Context(), harness.Interaction, harness.Responder); err != nil {
				t.Fatal(err)
			}

			initial := harness.InitialResponses()
			if len(initial) != 1 {
				t.Fatal("interaction acknowledged twice")
			}

			var embeds []api.Embed
			switch mode {
			case "unacknowledged":
				assertNoMentions(t, initial[0].Response.Data.AllowedMentions)
				if initial[0].Response.Data.Flags&api.MessageFlagEphemeral == 0 {
					t.Fatal("initial error was public")
				}
				embeds = initial[0].Response.Data.Embeds
			case "deferred public", "deferred ephemeral":
				if len(harness.OriginalEdits()) != 1 || len(harness.Followups()) != 0 {
					t.Fatal("error did not complete the deferred response")
				}

				if ephemeral := initial[0].Response.Data.Flags&api.MessageFlagEphemeral != 0; ephemeral != (mode == "deferred ephemeral") {
					t.Fatal("deferral visibility changed")
				}
				embeds = *harness.OriginalEdits()[0].Embeds
				assertNoMentions(t, harness.OriginalEdits()[0].AllowedMentions)
			case "replied", "completed deferral":
				expectedEdits := 0
				if mode == "completed deferral" {
					expectedEdits = 1
				}

				if len(harness.OriginalEdits()) != expectedEdits || len(harness.Followups()) != 1 || harness.Followups()[0].Message.Flags&api.MessageFlagEphemeral == 0 {
					t.Fatal("error overwrote successful output or produced a public follow-up")
				}
				embeds = harness.Followups()[0].Message.Embeds
				assertNoMentions(t, harness.Followups()[0].Message.AllowedMentions)
			}

			if len(embeds) != 1 || *embeds[0].Description != "Invalid selection" {
				t.Fatal("public error message changed")
			}
		})
	}
}

func TestFailedDeferralDoesNotRetryOrRunCommand(t *testing.T) {
	failure := errors.New("acknowledgement lost")
	transport := &failedAcknowledgement{
		err: failure,
	}
	i := testkit.CommandInteraction("price")
	responder := interactions.NewResponder(transport, i)
	_, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
	})
	err := app.DispatchInteraction(t.Context(), i, responder)
	if !errors.Is(err, failure) || transport.calls != 1 {
		t.Fatalf("failed deferral was retried or masked: %v", err)
	}

	if _, ok := errors.AsType[*interactions.ResponseError](err); !ok {
		t.Fatal("response operation lost its typed error")
	}
}

func TestCentralUnexpectedErrorsIncludeLoggedReference(t *testing.T) {
	for _, mode := range []string{"returned", "panic", "decoding"} {
		t.Run(mode, func(t *testing.T) {
			b, app := newTestHandler(t, &tracker.Service{
				Config: testutil.Config(t),
			})
			var logs bytes.Buffer
			b.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			binding, err := tomogocommands.NewBinding(api.ApplicationCommand{
				Name:        "failure",
				Description: "Test central failures",
			}, func(tomogocommands.Input) (struct{}, error) {
				if mode == "decoding" {
					return struct{}{}, errors.New("private decoding failure")
				}

				return struct{}{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}

			_, err = binding.Register(app.Commands(), func(context.Context, tomogocommands.Call[struct{}]) error {
				if mode == "panic" {
					panic("private panic failure")
				}

				if mode == "decoding" {
					t.Fatal("handler ran after decoding failed")
				}

				return errors.New("private returned failure")
			})
			if err != nil {
				t.Fatal(err)
			}

			harness := testkit.NewInteractionHarness(testkit.CommandInteraction("failure"))
			if err := app.DispatchInteraction(t.Context(), harness.Interaction, harness.Responder); err != nil {
				t.Fatal(err)
			}

			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("expected exactly one failure log: %v", err)
			}

			if mode == "panic" && strings.Contains(logs.String(), "private panic failure") {
				t.Fatal("panic logging exposed the recovered value")
			}

			reference, ok := entry["reference"].(string)
			if !ok || !cuid.IsValidLength(reference, 24) || entry["command"] != "failure" || entry["msg"] != "command failed" {
				t.Fatalf("failure lost reference or command attribution: %v", entry)
			}

			initial := harness.InitialResponses()
			if len(initial) != 1 || len(initial[0].Response.Data.Embeds) != 1 {
				t.Fatal("missing generic error response")
			}

			message := *initial[0].Response.Data.Embeds[0].Description
			if message != "I couldn't complete that request. Please try again. Reference: `"+reference+"`." {
				t.Fatal("generic error message changed or exposed internal details")
			}
		})
	}
}

func TestCentralResponseFailuresDoNotCascade(t *testing.T) {
	for _, mode := range []string{"handler followup", "error callback"} {
		t.Run(mode, func(t *testing.T) {
			_, app := newTestHandler(t, &tracker.Service{
				Config: testutil.Config(t),
			})
			failure := errors.New("ambiguous response delivery")
			business := domain.Invalid("Invalid selection")
			err := app.Commands().Register(api.ApplicationCommand{
				Name:        "failure",
				Description: "Test response failure",
			}, func(ctx context.Context, _ *api.Interaction, responder *interactions.Responder) error {
				if mode == "error callback" {
					return business
				}

				if _, err := responder.Message(ctx, interactions.Response("Successful output")); err != nil {
					return err
				}

				_, err := responder.Followup(ctx, interactions.Webhook("Next page"))

				return err
			})
			if err != nil {
				t.Fatal(err)
			}

			ack := &failedAcknowledgement{
				err: failure,
			}
			followup := &failedFollowup{
				err: failure,
			}
			transport := interactions.Transport(ack)
			if mode == "handler followup" {
				transport = followup
			}
			interaction := testkit.CommandInteraction("failure")
			responder := interactions.NewResponder(transport, interaction)
			err = app.DispatchInteraction(t.Context(), interaction, responder)
			if !errors.Is(err, failure) || ack.calls+followup.calls != 1 {
				t.Fatalf("response failure was retried or lost: %v", err)
			}

			if _, ok := errors.AsType[*interactions.ResponseError](err); !ok {
				t.Fatal("response failure lost its operation type")
			}

			if mode == "error callback" && !errors.Is(err, business) {
				t.Fatal("error callback failure lost the original business error")
			}
		})
	}
}

func TestCentralTimeoutErrorUsesBoundedResponseContext(t *testing.T) {
	_, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := app.Commands().Register(api.ApplicationCommand{
		Name:        "timeout",
		Description: "Test response context",
	}, func(ctx context.Context, _ *api.Interaction, responder *interactions.Responder) error {
		if err := responder.Defer(ctx, true); err != nil {
			return err
		}

		workCtx, workCancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		defer workCancel()
		cancel()

		return workCtx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	transport := &responseContextProbe{
		t: t,
	}
	interaction := testkit.CommandInteraction("timeout")
	responder := interactions.NewResponder(transport, interaction)
	if err := app.DispatchInteraction(ctx, interaction, responder); err != nil {
		t.Fatal(err)
	}

	if transport.initial != 1 || transport.edits != 1 {
		t.Fatal("timeout did not complete the deferred response")
	}
}

func TestTrackHandlerSendsEveryPage(t *testing.T) {
	db, _ := testutil.Database(t)
	_, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
		Store:  db,
	})
	for id := int64(1); id <= 25; id++ {
		price := testutil.Price(id, 2000, time.Now())
		price.Name = strings.Repeat("Long game name ", 15)
		_, err := db.Add(t.Context(), store.AddRequest{
			OwnerID:   "123",
			Price:     price,
			Condition: domain.AnySale,
			Maximum:   25,
			Interval:  time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	harness := dispatch(t, app, "track", testkit.Subcommand("list"))
	if len(harness.Followups()) == 0 {
		t.Fatal("track list omitted overflow pages")
	}
	count := strings.Count(*(*harness.OriginalEdits()[0].Embeds)[0].Description, "any sale")
	for _, followup := range harness.Followups() {
		assertNoMentions(t, followup.Message.AllowedMentions)
		if followup.Message.Flags&api.MessageFlagEphemeral == 0 {
			t.Fatal("track list exposed an overflow page publicly")
		}

		for _, embed := range followup.Message.Embeds {
			count += strings.Count(*embed.Description, "any sale")
		}
	}
	if count != 25 {
		t.Fatalf("expected each rule exactly once, got %d", count)
	}
}

func TestDefaultRouterLeavesUnmatchedInteractionsUnanswered(t *testing.T) {
	_, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
	})
	for _, interaction := range []*api.Interaction{
		testkit.ComponentInteraction("unrelated"),
		testkit.AutocompleteInteraction("unrelated", testkit.FocusedStringOption("query", "game")),
	} {
		harness := testkit.NewInteractionHarness(interaction)
		err := app.DispatchInteraction(t.Context(), interaction, harness.Responder)
		if interaction.Type == api.InteractionMessageComponent {
			if !errors.Is(err, components.ErrNoRoute) {
				t.Fatalf("unmatched component error = %v", err)
			}
		} else if err != nil {
			t.Fatalf("unmatched autocomplete error = %v", err)
		}

		if len(harness.InitialResponses()) != 0 || len(harness.Followups()) != 0 {
			t.Fatal("ignored interaction received a response")
		}
	}
}

func TestCentralErrorPreservesOtherListenerFailures(t *testing.T) {
	for _, name := range []string{"public error", "public and unexpected errors"} {
		t.Run(name, func(t *testing.T) {
			b, app := newTestHandler(t, &tracker.Service{
				Config: testutil.Config(t),
			})
			b.Tracker.Config.PriceChecksEnabled = false
			var logs bytes.Buffer
			b.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			unexpected := name == "public and unexpected errors"
			if unexpected {
				_, err := app.Events().OnInteractionObserved(func(context.Context, events.Event[execution.InteractionView]) error {
					return errors.New("private listener failure")
				})
				if err != nil {
					t.Fatal(err)
				}
			}

			harness := dispatch(t, app, "price")
			message := *(*harness.OriginalEdits()[0].Embeds)[0].Description
			if unexpected {
				if !strings.Contains(message, "Reference:") || strings.Contains(message, "private listener failure") {
					t.Fatal("combined failure was masked by a public error or exposed internal details")
				}

				var entry map[string]any
				if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
					t.Fatalf("expected one combined failure log: %v", err)
				}

				if !strings.Contains(fmt.Sprint(entry["error"]), "private listener failure") {
					t.Fatal("unexpected listener error was lost")
				}
			} else if message != "Price checks are temporarily paused for maintenance." || logs.Len() != 0 {
				t.Fatal("single wrapped public error should retain its message without an error log")
			}
		})
	}
}

func TestCentralErrorLeavesUnsupportedInteractionsForReporting(t *testing.T) {
	b, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
	})
	var logs bytes.Buffer
	b.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	failure := errors.New("observer failed")
	_, err := app.Events().OnInteractionObserved(func(context.Context, events.Event[execution.InteractionView]) error {
		return failure
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, interaction := range []*api.Interaction{
		nil,
		testkit.ComponentInteraction("unrelated"),
		testkit.AutocompleteInteraction("unrelated", testkit.FocusedStringOption("query", "game")),
	} {
		harness := testkit.NewInteractionHarness(interaction)
		err := app.DispatchInteraction(t.Context(), interaction, harness.Responder)
		if err == nil {
			t.Fatal("unhandled error was swallowed")
		}

		if interaction != nil && !errors.Is(err, failure) {
			t.Fatal("observer error was lost")
		}

		if len(harness.InitialResponses()) != 0 || logs.Len() != 0 {
			t.Fatal("unsupported interaction attempted a command error response or duplicate logging")
		}

		if _, ok := errors.AsType[*registration.PanicError](err); ok {
			t.Fatal("error callback panicked while reporting an unsupported interaction")
		}

		if _, ok := errors.AsType[*interactions.ResponseError](err); ok {
			t.Fatal("error callback attempted a response to an unsupported interaction")
		}
	}
}

func TestCentralMalformedCommandData(t *testing.T) {
	for _, mode := range []string{"absent", "typed nil"} {
		t.Run(mode, func(t *testing.T) {
			h, app := newTestHandler(t, &tracker.Service{
				Config: testutil.Config(t),
			})
			var logs bytes.Buffer
			h.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			interaction := testkit.CommandInteraction("about")
			interaction.Data = nil
			if mode == "typed nil" {
				interaction.Data = (*api.CommandInteractionData)(nil)
			}
			harness := testkit.NewInteractionHarness(interaction)
			if err := app.DispatchInteraction(t.Context(), interaction, harness.Responder); err != nil {
				t.Fatalf("malformed command escaped the error boundary: %v", err)
			}

			initial := harness.InitialResponses()
			if len(initial) != 1 || initial[0].Response.Data.Flags&api.MessageFlagEphemeral == 0 || len(initial[0].Response.Data.Embeds) != 1 {
				t.Fatal("malformed command did not receive one private error response")
			}

			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("expected one malformed-input log: %v", err)
			}

			if strings.Contains(fmt.Sprint(entry["error"]), "handler panic") {
				t.Fatal("malformed command triggered a panic")
			}

			reference, ok := entry["reference"].(string)
			if !ok || !cuid.IsValidLength(reference, 24) || !strings.Contains(*initial[0].Response.Data.Embeds[0].Description, reference) {
				t.Fatal("malformed command lost its logged reference")
			}
		})
	}
}

func TestCentralPreconditionDenials(t *testing.T) {
	for _, mode := range []string{"denied", "denied with listener failure"} {
		t.Run(mode, func(t *testing.T) {
			h, app := newTestHandler(t, &tracker.Service{
				Config: testutil.Config(t),
			})
			var logs bytes.Buffer
			h.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			binding, err := tomogocommands.NewBinding(api.ApplicationCommand{
				Name:        "restricted",
				Description: "Exercise framework preconditions",
			}, func(tomogocommands.Input) (struct{}, error) {
				t.Error("denied command reached argument decoding")

				return struct{}{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			command := tomogocommands.Command[struct{}]{
				Binding: binding,
				Preconditions: []preconditions.Check{
					preconditions.Func(func(context.Context, *api.Interaction) error {
						return &preconditions.Failure{
							Reason: "This command is temporarily unavailable.",
						}
					}),
				},
				Handler: func(context.Context, tomogocommands.Call[struct{}]) error {
					t.Error("denied command handler ran")

					return nil
				},
			}
			if _, err := command.Register(app.Commands()); err != nil {
				t.Fatal(err)
			}

			if mode == "denied with listener failure" {
				if _, err := app.Events().OnInteractionObserved(func(context.Context, events.Event[execution.InteractionView]) error {
					return errors.New("private observer failure")
				}); err != nil {
					t.Fatal(err)
				}
			}

			harness := dispatch(t, app, "restricted")
			message := *harness.InitialResponses()[0].Response.Data.Embeds[0].Description
			if mode == "denied" {
				if message != "This command is temporarily unavailable." || logs.Len() != 0 {
					t.Fatal("expected denial was treated as an unexpected failure")
				}
			} else if !strings.Contains(message, "Reference:") || strings.Contains(message, "private observer failure") || !strings.Contains(logs.String(), "private observer failure") {
				t.Fatal("precondition denial masked or exposed another listener's failure")
			}
		})
	}
}
