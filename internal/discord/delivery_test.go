package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/rest"
)

type artworkStub struct {
	price domain.Price
	err   error
}

func (s artworkStub) Artwork(context.Context, int64, string) (string, error) {
	return s.price.CapsuleURL, s.err
}

func TestDiscordDelivery(t *testing.T) {
	var sent api.MessageCreate
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/users/@me/channels":
			fmt.Fprint(w, `{"id":"456","type":1}`)
		case "/channels/456/messages":
			sent = api.MessageCreate{}
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Error(err)
			}
			fmt.Fprint(w, `{"id":"789","channel_id":"456"}`)
		default:
			t.Error("unexpected Discord route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	sender := Sender{
		Steam: artworkStub{
			price: domain.Price{
				CapsuleURL: "https://shared.akamai.steamstatic.com/store_item_assets/steam/apps/10/version/header.jpg?t=123",
			},
		},
		REST: rest.New(rest.Config{
			Token:   "test-token",
			BaseURL: server.URL,
		}),
	}
	id, err := sender.Send(t.Context(), "123", "abcdefghijklmnopqrstuvwx", domain.Payload{
		AppID:    10,
		Name:     strings.Repeat("🦊", 400),
		Country:  "US",
		Currency: "USD",
		Regular:  2000,
		Final:    1000,
		Discount: 50,
		RuleID:   "abcdefghijklmnopqrstuvwx",
	})
	if err != nil || id != "789" {
		t.Fatalf("send: %s, %v", id, err)
	}

	if len(sent.Embeds) != 1 || sent.AllowedMentions == nil || !sent.EnforceNonce || sent.Nonce != "abcdefghijklmnopqrstuvwx" {
		t.Fatalf("unexpected outgoing message: %+v", sent)
	}

	if sent.AllowedMentions.Parse == nil || len(sent.AllowedMentions.Parse) != 0 {
		t.Fatal("sale notification must explicitly disable mention parsing")
	}

	if sent.Embeds[0].Image == nil || sent.Embeds[0].Image.URL != "https://shared.akamai.steamstatic.com/store_item_assets/steam/apps/10/version/header.jpg?t=123" {
		t.Fatalf("sale notification lost its capsule: %+v", sent.Embeds[0])
	}

	sender.Steam = artworkStub{
		err: errors.New("Steam temporarily unavailable"),
	}
	_, err = sender.Send(t.Context(), "123", "abcdefghijklmnopqrstuvwx", domain.Payload{
		AppID: 10,
		Name:  "Game",
	})
	if err != nil || len(sent.Embeds) != 1 || sent.Embeds[0].Image != nil {
		t.Fatalf("missing artwork blocked notification delivery: %v, %+v", err, sent)
	}
}

func TestDMDeliveryFailures(t *testing.T) {
	for _, kind := range []string{"sale", "free game"} {
		for _, stage := range []string{"open DM", "send message"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				var opens, sends int
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/users/@me/channels":
						opens++
						if stage == "send message" {
							fmt.Fprint(w, `{"id":"456","type":1}`)
							return
						}
					case "/channels/456/messages":
						sends++
					default:
						t.Error("unexpected Discord route", r.URL.Path)
					}
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"code":50007,"message":"Cannot send messages to this user"}`)
				}))
				defer server.Close()
				sender := Sender{
					REST: rest.New(rest.Config{
						Token:   "test-token",
						BaseURL: server.URL,
					}),
				}
				var id string
				var err error
				if kind == "sale" {
					id, err = sender.Send(t.Context(), "123", "abcdefghijklmnopqrstuvwx", domain.Payload{
						AppID: 10,
						Name:  "Game",
					})
				} else {
					id, err = sender.SendFree(t.Context(), "dm", "123", "abcdefghijklmnopqrstuvwx", freegames.Offer{
						Source:   "gog",
						Title:    "Game",
						URL:      "https://www.gog.com/en/game/test",
						ImageURL: "https://images.gog-statics.com/test.jpg",
					})
				}
				remote, ok := errors.AsType[*rest.Error](err)
				if !ok || remote.StatusCode != http.StatusForbidden || remote.Code != rest.CodeCannotSendMessagesToUser || id != "" {
					t.Fatalf("delivery lost its Discord failure: id=%q, error=%v", id, err)
				}
				wantSends := 0
				if stage == "send message" {
					wantSends = 1
				}

				if opens != 1 || sends != wantSends {
					t.Fatalf("unexpected retry or send after failed DM creation: opens=%d, sends=%d", opens, sends)
				}
			})
		}
	}
}
