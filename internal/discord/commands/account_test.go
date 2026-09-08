package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/depthbomb/dealfox/ent/rule"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/store"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/events"
	"github.com/depthbomb/tomogo/resource"
	"github.com/depthbomb/tomogo/rest"
	"github.com/depthbomb/tomogo/testkit"
)

type accountHTTP struct {
	mu      sync.Mutex
	blocked bool
	prompts int
	result  string
}

func (f *accountHTTP) Do(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body := `{"id":"777","type":1}`
	status := 200
	switch request.Method + " " + request.URL.Path {
	case "POST /users/@me/channels":
		if f.blocked {
			status = 403
			body = `{"code":50007,"message":"Cannot send messages to this user"}`
		}
	case "POST /channels/777/messages":
		f.prompts++
		body = `{"id":"100","channel_id":"777"}`
	case "PATCH /channels/777/messages/100":
		var edit api.MessageEdit
		if err := json.NewDecoder(request.Body).Decode(&edit); err != nil {
			return nil, err
		}
		f.result = *(*edit.Embeds)[0].Description
		body = `{"id":"100","channel_id":"777"}`
	default:
		status = 404
	}

	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func accountInteraction() *api.Interaction {
	i := testkit.CommandInteraction("account", testkit.Subcommand("delete"))
	i.User = &api.User{
		ID: 123,
	}

	return i
}

func agreement() api.Message {
	return api.Message{
		ID:        101,
		ChannelID: 777,
		Author: api.User{
			ID: 123,
		},
		Content: "I agree",
	}
}

func TestAccountConfirmationExpiresAndIgnoresInvalidReplies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, app := newTestHandler(t, &tracker.Service{
			Config: testutil.Config(t),
		})
		transport := &accountHTTP{}
		h.REST = rest.New(rest.Config{
			Token:      "test",
			BaseURL:    "https://discord.invalid",
			HTTPClient: transport,
		})
		i := accountInteraction()
		harness := testkit.NewInteractionHarness(i)
		done := make(chan error, 1)
		go func() {
			done <- app.DispatchInteraction(t.Context(), i, harness.Responder)
		}()
		synctest.Wait()
		if transport.prompts != 1 {
			t.Fatal("confirmation DM was not sent")
		}

		eventHarness := testkit.NewEventHarness(app.Events(), 0)
		for _, change := range []func(*api.Message){
			func(m *api.Message) {
				m.Author.ID = 999
			},
			func(m *api.Message) {
				m.ChannelID = 999
			},
			func(m *api.Message) {
				m.ID = 99
			},
			func(m *api.Message) {
				m.Content = "i agree"
			},
			func(m *api.Message) {
				m.Content = "I agree "
			},
			func(m *api.Message) {
				m.Author.Bot = true
			},
			func(m *api.Message) {
				m.WebhookID = 999
			},
		} {
			message := agreement()
			change(&message)
			if err := testkit.DispatchEvent(t.Context(), eventHarness, events.MessageCreateEvent(), message); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("invalid reply ended confirmation: %v", err)
		default:
		}

		duplicate := invokeLimitedCommand(t, app, 123, 0, "account", testkit.Subcommand("delete"))
		if len(duplicate.OriginalEdits()) != 0 || duplicate.InitialResponses()[0].Response.Data.Flags&api.MessageFlagEphemeral == 0 || transport.prompts != 1 {
			t.Fatal("duplicate confirmation was not rejected privately")
		}

		time.Sleep(deletionConfirmationTimeout)
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("confirmation did not finish at the consent deadline")
		}

		if !strings.Contains(transport.result, "has not been deleted") || len(harness.OriginalEdits()) != 2 || len(h.deletions.users) != 0 {
			t.Fatal("timeout did not cancel and clean up confirmation")
		}

		for _, registration := range app.Events().Registrations() {
			if registration.Name == events.EventMessageCreate {
				t.Fatal("confirmation listener leaked after timeout")
			}
		}

		if err := testkit.DispatchEvent(t.Context(), eventHarness, events.MessageCreateEvent(), agreement()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAccountDeletionRequiresConfirmedReply(t *testing.T) {
	db, _ := testutil.Database(t)
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
		transport := &accountHTTP{}
		h.REST = rest.New(rest.Config{
			Token:      "test",
			BaseURL:    "https://discord.invalid",
			HTTPClient: transport,
		})
		i := accountInteraction()
		harness := testkit.NewInteractionHarness(i)
		done := make(chan error, 1)
		go func() {
			done <- app.DispatchInteraction(t.Context(), i, harness.Responder)
		}()
		synctest.Wait()
		if db.Client.Rule.Query().Where(rule.OwnerIDEQ("123")).CountX(t.Context()) != 1 {
			t.Fatal("data was removed before consent")
		}

		time.Sleep(deletionConfirmationTimeout - time.Second)
		eventHarness := testkit.NewEventHarness(app.Events(), 0)
		if err := testkit.DispatchEvent(t.Context(), eventHarness, events.MessageCreateEvent(), agreement()); err != nil {
			t.Fatal(err)
		}

		if err := <-done; err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if !strings.Contains(transport.result, "have been deleted") || db.Client.Rule.Query().Where(rule.OwnerIDEQ("123")).CountX(t.Context()) != 0 || len(h.deletions.users) != 0 {
			t.Fatal("confirmed deletion did not finish")
		}

		initial := harness.InitialResponses()
		if len(initial) != 1 || initial[0].Response.Data.Flags&api.MessageFlagEphemeral == 0 || len(harness.Followups()) != 0 {
			t.Fatal("account command exposed its interaction response")
		}
	})
}

func TestAccountBlockedDMKeepsData(t *testing.T) {
	h, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
	})
	transport := &accountHTTP{
		blocked: true,
	}
	h.REST = rest.New(rest.Config{
		Token:      "test",
		BaseURL:    "https://discord.invalid",
		HTTPClient: transport,
	})
	harness := invokeLimitedCommand(t, app, 123, 456, "account", testkit.Subcommand("delete"))
	if !strings.Contains(*(*harness.OriginalEdits()[0].Embeds)[0].Description, "Nothing was deleted") || transport.prompts != 0 || len(h.deletions.users) != 0 {
		t.Fatal("blocked DM did not leave the account untouched")
	}
}

func TestConfirmationRejectsBoundaryAndPreviousPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		confirmation := &deletionConfirmation{
			user:    123,
			channel: 777,
		}
		var resources *resource.Service
		message, err := resources.BindMessage(agreement())
		if err != nil {
			t.Fatal(err)
		}
		event := events.MessageCreate{
			Data: message,
		}
		if confirmation.accepts(event) {
			t.Fatal("confirmation accepted before the prompt was sent")
		}
		confirmation.arm(100)
		for _, missing := range []events.MessageCreate{
			{},
			{
				Data: &resource.Message{
					Message: agreement(),
				},
			},
		} {
			if confirmation.accepts(missing) {
				t.Fatal("confirmation accepted without a connected message author")
			}
		}

		if !confirmation.accepts(event) {
			t.Fatal("fresh agreement was rejected")
		}
		time.Sleep(deletionConfirmationTimeout - time.Nanosecond)
		if !confirmation.accepts(event) {
			t.Fatal("agreement before the deadline was rejected")
		}
		time.Sleep(time.Nanosecond)
		if confirmation.accepts(event) {
			t.Fatal("agreement at the deadline was accepted")
		}
		confirmation.arm(102)
		if confirmation.accepts(event) {
			t.Fatal("old agreement confirmed a new prompt")
		}
	})
}
