package commands

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/testkit"
	"github.com/depthbomb/tomogo/webhookhttp"
)

type httpResponseTransport struct {
	interactions.Transport
	edits chan api.MessageEdit
}

func (t *httpResponseTransport) EditOriginal(_ context.Context, _ api.ID, _ string, edit api.MessageEdit) (*api.Message, error) {
	t.edits <- edit

	return &api.Message{}, nil
}

func TestHTTPCommandRoutingAndCentralErrors(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"about", "price"} {
		t.Run(name, func(t *testing.T) {
			b, app := newTestHandler(t, &tracker.Service{
				Config: testutil.Config(t),
			})
			b.Tracker.Config.PriceChecksEnabled = false
			transport := &httpResponseTransport{
				edits: make(chan api.MessageEdit, 1),
			}
			reports := make(chan error, 2)
			handler, err := app.InteractionHTTPHandler(webhookhttp.InteractionHandler{
				Verifier: webhookhttp.Verifier{
					PublicKey: public,
				},
				Followups: transport,
				HandleError: func(err error) {
					reports <- err
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			dispatch := handler.Dispatch
			handler.Dispatch = func(ctx context.Context, i *api.Interaction, responder *interactions.Responder) error {
				err := dispatch(ctx, i, responder)
				done <- err

				return err
			}
			interaction := testkit.CommandInteraction(name)
			interaction.User = &api.User{
				ID: 123,
			}
			body, err := json.Marshal(interaction)
			if err != nil {
				t.Fatal(err)
			}
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			signature := ed25519.Sign(private, append([]byte(timestamp), body...))
			request := httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader(body))
			request.Header.Set("X-Signature-Timestamp", timestamp)
			request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("interaction status = %d", recorder.Code)
			}

			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("HTTP command dispatch did not finish")
			}

			var initial api.InteractionResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &initial); err != nil {
				t.Fatal(err)
			}

			if name == "about" {
				if initial.Type != api.CallbackChannelMessage || initial.Data.Flags&api.MessageFlagEphemeral != 0 || len(initial.Data.Embeds) != 1 || len(transport.edits) != 0 {
					t.Fatal("HTTP about response lost its immediate public embed")
				}
			} else {
				if initial.Type != api.CallbackDeferredChannelMessage || initial.Data.Flags&api.MessageFlagEphemeral == 0 || len(transport.edits) != 1 {
					t.Fatal("HTTP price error did not complete its private deferral")
				}
				edit := <-transport.edits
				if edit.Embeds == nil || len(*edit.Embeds) != 1 || *(*edit.Embeds)[0].Description != "Price checks are temporarily paused for maintenance." {
					t.Fatal("HTTP dispatch lost the central public error response")
				}
			}

			select {
			case err := <-reports:
				t.Fatalf("handled command produced an HTTP error report: %v", err)
			default:
			}
		})
	}
}
