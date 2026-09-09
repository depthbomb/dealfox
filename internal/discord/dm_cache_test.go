package discord

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/freegames"
	"github.com/depthbomb/tomogo/rest"
)

type deliveryHTTP func(*http.Request) (*http.Response, error)

func (f deliveryHTTP) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func deliveryResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func cachedDelivery(t *testing.T, transport deliveryHTTP, config rest.DMCacheConfig) Sender {
	t.Helper()
	client := rest.New(rest.Config{
		Token:      "test",
		BaseURL:    "https://discord.invalid",
		HTTPClient: transport,
	})

	return Sender{
		REST: client,
		DM:   testDMSender(t, client, config),
	}
}

func sendTestSale(ctx context.Context, sender Sender, user string) error {
	_, err := sender.Send(ctx, user, "abcdefghijklmnopqrstuvwx", domain.Payload{
		AppID: 10,
		Name:  "Game",
	})

	return err
}

func sendTestFree(ctx context.Context, sender Sender) error {
	_, err := sender.SendFree(ctx, "dm", "123", "bcdefghijklmnopqrstuvwxy", freegames.Offer{
		Source:   "gog",
		Title:    "Game",
		URL:      "https://www.gog.com/en/game/test",
		ImageURL: "https://images.gog-statics.com/test.jpg",
	})

	return err
}

func TestNotificationsShareDMCacheAndExpire(t *testing.T) {
	var opens, sends int
	now := time.Now()
	sender := cachedDelivery(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/users/@me/channels" {
			opens++

			return deliveryResponse(`{"id":"456","type":1}`), nil
		}
		sends++

		return deliveryResponse(`{"id":"789","channel_id":"456"}`), nil
	}, rest.DMCacheConfig{
		Capacity: 1,
		TTL:      time.Hour,
		Now: func() time.Time {
			return now
		},
	})
	if err := sendTestSale(t.Context(), sender, "123"); err != nil {
		t.Fatal(err)
	}

	if err := sendTestFree(t.Context(), sender); err != nil {
		t.Fatal(err)
	}

	if opens != 1 || sends != 2 {
		t.Fatalf("sale/free cache not shared: opens=%d sends=%d", opens, sends)
	}
	now = now.Add(time.Hour)
	if err := sendTestFree(t.Context(), sender); err != nil {
		t.Fatal(err)
	}

	if opens != 2 {
		t.Fatal("expired DM channel was retained")
	}

	if err := sendTestSale(t.Context(), sender, "999"); err != nil {
		t.Fatal(err)
	}

	if err := sendTestFree(t.Context(), sender); err != nil {
		t.Fatal(err)
	}

	if opens != 4 || sends != 5 {
		t.Fatal("bounded cache did not evict the previous user")
	}
}

func TestNotificationDMOpenCoalescingAndCancellation(t *testing.T) {
	for _, mode := range []string{"success", "waiter", "owner"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cancelOwner := mode == "owner"
				var opens, sends atomic.Int32
				release := make(chan struct{})
				sender := cachedDelivery(t, func(request *http.Request) (*http.Response, error) {
					if request.URL.Path == "/users/@me/channels" {
						opens.Add(1)
						select {
						case <-release:
							return deliveryResponse(`{"id":"456","type":1}`), nil
						case <-request.Context().Done():
							return nil, request.Context().Err()
						}
					}
					sends.Add(1)

					return deliveryResponse(`{"id":"789","channel_id":"456"}`), nil
				}, rest.DMCacheConfig{
					Capacity: 1,
					TTL:      time.Hour,
				})
				ownerCtx, cancelOwnerCtx := context.WithCancel(t.Context())
				defer cancelOwnerCtx()
				owner := make(chan error, 1)
				go func() {
					owner <- sendTestSale(ownerCtx, sender, "123")
				}()
				synctest.Wait()
				waiterCtx, cancelWaiter := context.WithCancel(t.Context())
				defer cancelWaiter()
				waiter := make(chan error, 1)
				go func() {
					waiter <- sendTestFree(waiterCtx, sender)
				}()
				synctest.Wait()
				if opens.Load() != 1 {
					t.Fatal("concurrent sends opened duplicate channels")
				}

				if cancelOwner {
					cancelOwnerCtx()
					if err := <-owner; !errors.Is(err, context.Canceled) {
						t.Fatalf("owner cancellation: %v", err)
					}

					if err := <-waiter; !errors.Is(err, context.Canceled) {
						t.Fatalf("waiter lost open failure: %v", err)
					}
				} else if mode == "waiter" {
					cancelWaiter()
					if err := <-waiter; !errors.Is(err, context.Canceled) {
						t.Fatalf("waiter cancellation: %v", err)
					}
					close(release)
					if err := <-owner; err != nil {
						t.Fatal(err)
					}
				} else {
					close(release)
					if err := <-owner; err != nil {
						t.Fatal(err)
					}

					if err := <-waiter; err != nil {
						t.Fatal(err)
					}

					if sends.Load() != 2 {
						t.Fatal("coalescing lost one notification")
					}
				}

				if cancelOwner {
					if sends.Load() != 0 {
						t.Fatal("cancelled open sent a message")
					}
					close(release)
				}

				if err := sendTestFree(t.Context(), sender); err != nil {
					t.Fatal("cache unusable after cancellation", err)
				}
				wantOpens := int32(1)
				if cancelOwner {
					wantOpens = 2
				}

				if opens.Load() != wantOpens {
					t.Fatal("cache lost a successful open or retained a cancelled one")
				}
			})
		})
	}
}
