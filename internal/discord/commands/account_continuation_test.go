package commands

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/depthbomb/dealfox/ent/rule"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/collector"
	"github.com/depthbomb/tomogo/continuation"
	"github.com/depthbomb/tomogo/events"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/rest"
	"github.com/depthbomb/tomogo/testkit"
)

func configureAccountHTTP(t *testing.T, h *Handler, transport *accountHTTP) {
	t.Helper()
	h.REST = rest.New(rest.Config{
		Token:      "test",
		BaseURL:    "https://discord.invalid",
		HTTPClient: transport,
	})
	var err error
	h.DM, err = rest.NewDMSender(h.REST, rest.DMCacheConfig{
		Capacity: 4,
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func pendingAccounts(h *Handler) int {
	h.deletions.mu.Lock()
	defer h.deletions.mu.Unlock()

	return len(h.deletions.users)
}

func TestAccountContinuationOutlivesDispatchAndDrainsOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, app := newTestHandler(t, &tracker.Service{
			Config: testutil.Config(t),
		})
		cleanupStarted := make(chan struct{})
		allowCleanup := make(chan struct{})
		transport := &accountHTTP{
			onResult: func(request *http.Request) {
				if request.Context().Err() != nil {
					t.Error("cleanup inherited cancellation")
				}
				deadline, ok := request.Context().Deadline()
				if !ok || time.Until(deadline) > 10*time.Second {
					t.Error("cleanup is unbounded")
				}
				close(cleanupStarted)
				<-allowCleanup
			},
		}
		configureAccountHTTP(t, h, transport)
		i := accountInteraction()
		harness := testkit.NewInteractionHarness(i)
		dispatchCtx, cancelDispatch := context.WithCancel(t.Context())
		if err := app.DispatchInteraction(dispatchCtx, i, harness.Responder); err != nil {
			t.Fatal(err)
		}
		cancelDispatch()
		synctest.Wait()
		if pendingAccounts(h) != 1 || transport.prompts != 1 {
			t.Fatal("dispatch cancellation ended owned confirmation")
		}
		if _, err := harness.Responder.EditOriginal(t.Context(), api.MessageEdit{}); !errors.Is(err, interactions.ErrTransferred) {
			t.Fatalf("borrowed responder retained ownership: %v", err)
		}
		blocked := invokeLimitedCommand(t, app, 123, 0, "about")
		if !strings.Contains(*blocked.InitialResponses()[0].Response.Data.Embeds[0].Description, "deletion in progress") {
			t.Fatal("pending user was admitted to another command")
		}

		shutdown := make(chan error, 1)
		go func() {
			shutdown <- app.Continuations().Shutdown(t.Context())
		}()
		<-cleanupStarted
		if pendingAccounts(h) != 1 {
			t.Fatal("user released before cleanup completed")
		}
		select {
		case <-shutdown:
			t.Fatal("shutdown did not wait for cleanup")
		default:
		}
		close(allowCleanup)
		if err := <-shutdown; err != nil {
			t.Fatal(err)
		}
		if pendingAccounts(h) != 0 || len(harness.OriginalEdits()) != 2 {
			t.Fatal("shutdown did not release state and finish the response")
		}
		for _, registration := range app.Events().Registrations() {
			if registration.Name == events.EventMessageCreate {
				t.Fatal("collector leaked after shutdown")
			}
		}
	})
}

func TestAccountContinuationAdmissionAndSetupRollback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, app := newTestHandler(t, &tracker.Service{
			Config: testutil.Config(t),
		})
		configureAccountHTTP(t, h, &accountHTTP{})
		for user := api.ID(1); user <= 4; user++ {
			invokeLimitedCommand(t, app, user, 0, "account", testkit.Subcommand("delete"))
		}
		synctest.Wait()
		fifth := invokeLimitedCommand(t, app, 5, 0, "account", testkit.Subcommand("delete"))
		if fifth.InitialResponses()[0].Response.Type != api.CallbackChannelMessage || !strings.Contains(*fifth.InitialResponses()[0].Response.Data.Embeds[0].Description, "busy") || pendingAccounts(h) != 4 {
			t.Fatal("capacity rejection acknowledged a task or leaked per-user state")
		}
		if err := app.Continuations().Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		if pendingAccounts(h) != 0 {
			t.Fatal("shutdown leaked pending users")
		}
	})

	for _, stage := range []string{"acknowledgement", "start", "closed manager"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, app := newTestHandler(t, &tracker.Service{
					Config: testutil.Config(t),
				})
				if stage == "closed manager" {
					app.Continuations().Cancel()
				}
				transport := &accountAcknowledgement{
					manager:      app.Continuations(),
					fail:         stage == "acknowledgement",
					closeManager: stage == "start",
				}
				i := accountInteraction()
				responder := interactions.NewResponder(transport, i)
				_ = app.DispatchInteraction(t.Context(), i, responder)
				synctest.Wait()
				if pendingAccounts(h) != 0 {
					t.Fatal("setup failure leaked pending user")
				}
				if stage == "acknowledgement" {
					tickets := make([]*continuation.Ticket, 0, 4)
					for range 4 {
						ticket, err := app.Continuations().Reserve(t.Context(), continuation.Options{})
						if err != nil {
							t.Fatal("acknowledgement failure leaked admission", err)
						}
						tickets = append(tickets, ticket)
					}
					for _, ticket := range tickets {
						ticket.Rollback()
					}
				}
			})
		})
	}
}

type accountAcknowledgement struct {
	interactions.Transport
	manager      *continuation.Manager
	fail         bool
	closeManager bool
}

func (a *accountAcknowledgement) CreateResponse(context.Context, api.ID, string, api.InteractionResponse, bool) (*api.InteractionCallbackResponse, error) {
	if a.closeManager {
		a.manager.Cancel()
	}

	if a.fail {
		return nil, errors.New("acknowledgement failed")
	}

	return nil, nil
}

func (a *accountAcknowledgement) EditOriginal(context.Context, api.ID, string, api.MessageEdit) (*api.Message, error) {
	return &api.Message{}, nil
}

func TestAccountBuffersEarlyAgreementAndRejectsUncertainPrompt(t *testing.T) {
	db, _ := testutil.Database(t)
	for _, outcome := range []string{"success", "transport failure", "missing identity", "overflow"} {
		t.Run(outcome, func(t *testing.T) {
			_, err := db.Add(t.Context(), store.AddRequest{
				OwnerID:   "123",
				Price:     testutil.Price(10, 2000, time.Now()),
				Condition: domain.AnySale,
				Maximum:   25,
				Interval:  time.Hour,
			})
			if err != nil {
				t.Fatal(err)
			}
			synctest.Test(t, func(t *testing.T) {
				h, app := newTestHandler(t, &tracker.Service{
					Config: testutil.Config(t),
					Store:  db,
				})
				results := make(chan struct{}, 1)
				transport := &accountHTTP{
					onResult: func(*http.Request) {
						results <- struct{}{}
					},
				}
				if outcome == "missing identity" {
					transport.promptBody = "{}"
				}
				transport.onPrompt = func() error {
					harness := testkit.NewEventHarness(app.Events(), 0)
					count := 1
					if outcome == "overflow" {
						count = 17
					}
					for range count {
						err := testkit.DispatchEvent(t.Context(), harness, events.MessageCreateEvent(), agreement())
						if err != nil && !errors.Is(err, collector.ErrOverflow) {
							t.Error(err)
						}
					}
					if outcome == "transport failure" {
						return errors.New("prompt response lost")
					}

					return nil
				}
				configureAccountHTTP(t, h, transport)
				invokeLimitedCommand(t, app, 123, 0, "account", testkit.Subcommand("delete"))
				if outcome == "success" || outcome == "overflow" {
					<-results
				}
				synctest.Wait()
				if pendingAccounts(h) != 0 {
					t.Fatal("confirmation retained user after completion")
				}
				count := db.Client.Rule.Query().Where(rule.OwnerIDEQ("123")).CountX(t.Context())
				if (count == 0) != (outcome == "success") {
					t.Fatalf("%s: rules remaining = %d", outcome, count)
				}
				for _, registration := range app.Events().Registrations() {
					if registration.Name == events.EventMessageCreate {
						t.Fatal("collector leaked")
					}
				}
			})
			if err := db.DeleteAccount(t.Context(), "123"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
