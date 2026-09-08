package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/rest"
)

func TestFreeGameDeliveryRouting(t *testing.T) {
	dms := 0
	var sent api.MessageCreate
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/users/@me/channels":
			dms++
			var recipient struct {
				ID api.ID `json:"recipient_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&recipient); err != nil || recipient.ID != 123 {
				t.Errorf("incorrect DM recipient: %s, %v", recipient.ID, err)
			}
			fmt.Fprint(w, `{"id":"789","type":1}`)
		case "/channels/789/messages":
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Error(err)
			}
			fmt.Fprint(w, `{"id":"999","channel_id":"789"}`)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := rest.New(rest.Config{
		Token:   "test",
		BaseURL: server.URL,
	})
	offer := freegames.Offer{
		Source:      "gog",
		Title:       "Game",
		Description: "A game with *formatting*.",
		URL:         "https://www.gog.com/en/game/test",
		ImageURL:    "https://images.gog-statics.com/test.jpg",
		Platform:    "PC",
		EndsAt:      time.Now().Add(time.Hour),
	}
	sender := Sender{
		REST: client,
	}
	for _, kind := range []string{"channel", "dm"} {
		destination := "789"
		if kind == "dm" {
			destination = "123"
		}
		id, err := sender.SendFree(t.Context(), kind, destination, "abcdefghijklmnopqrstuvwx", offer)
		if err != nil || id != "999" {
			t.Fatalf("send: %s, %v", id, err)
		}
		if kind == "channel" && dms != 0 {
			t.Fatal("channel alert opened a DM")
		}

		if sent.AllowedMentions == nil || sent.AllowedMentions.Parse == nil || len(sent.AllowedMentions.Parse) != 0 {
			t.Fatal("free-game notification must explicitly disable mention parsing")
		}

		if !sent.EnforceNonce || sent.Nonce != "abcdefghijklmnopqrstuvwx" {
			t.Fatal("free-game notification lost duplicate prevention")
		}
	}
	if dms != 1 || len(sent.Embeds) != 1 || sent.Embeds[0].Image == nil || sent.Embeds[0].Image.URL != offer.ImageURL || sent.AllowedMentions == nil || !sent.EnforceNonce || !strings.Contains(*sent.Embeds[0].Description, "\\*formatting\\*") {
		t.Fatalf("incorrect free-game message: %+v", sent)
	}
}
