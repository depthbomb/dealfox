package steam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/testutil"
)

func expireArtwork(c *Client, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.artwork[key]
	entry.refreshAt = time.Now().Add(-time.Second)
	c.artwork[key] = entry
}

func TestArtworkSurvivesPriceExpiryAndSteamOutage(t *testing.T) {
	var requests atomic.Int64
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		if r.URL.Query().Get("filters") == "basic" {
			fmt.Fprint(w, `{"400":{"success":true,"data":{"steam_appid":400,"name":"Portal","type":"game","header_image":"https://example.com/version/header.jpg?t=123"}}}`)
		} else {
			fmt.Fprint(w, `{"400":{"success":true,"data":{"price_overview":{"currency":"USD","initial":1000,"final":500,"discount_percent":50}}}}`)
		}
	}))
	defer server.Close()
	client := New(testutil.Config(t))
	client.DetailsURL = server.URL
	price, err := client.Details(t.Context(), 400, "US")
	if err != nil || requests.Load() != 2 {
		t.Fatalf("initial details: %v, requests=%d", err, requests.Load())
	}

	client.mu.Lock()
	clear(client.cache)
	client.mu.Unlock()
	unavailable.Store(true)
	image, err := client.Artwork(t.Context(), 400, "us")
	if err != nil || image != price.CapsuleURL || requests.Load() != 2 {
		t.Fatalf("artwork depended on expired price data: %q, %v, requests=%d", image, err, requests.Load())
	}

	expireArtwork(client, "400:US")
	for range 3 {
		image, err = client.Artwork(t.Context(), 400, "US")
		if err != nil || image != price.CapsuleURL || requests.Load() != 3 {
			t.Fatalf("stale artwork/retry backoff failed: %q, %v, requests=%d", image, err, requests.Load())
		}
	}

	if _, err := client.Details(t.Context(), 400, "US"); err == nil {
		t.Fatal("artwork fallback allowed stale prices to be returned")
	}
}

func TestArtworkRefreshAndMissingImages(t *testing.T) {
	var requests atomic.Int64
	var mode atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("filters") != "basic" {
			t.Error("artwork requested price data")
		}

		switch mode.Load() {
		case 0:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 1:
			fmt.Fprint(w, `{"400":{"success":true,"data":{"steam_appid":400,"name":"Portal","header_image":"file:///invalid","capsule_image":"https://example.com/capsule.jpg"}}}`)
		case 2:
			fmt.Fprint(w, `{"400":{"success":true,"data":{"steam_appid":400,"name":"Portal"}}}`)
		case 3:
			fmt.Fprint(w, `{"400":{"success":true,"data":{"steam_appid":400,"name":"Portal","header_image":"https://example.com/new/header.jpg?t=456"}}}`)
		}
	}))
	defer server.Close()
	client := New(testutil.Config(t))
	client.DetailsURL = server.URL
	if image, err := client.Artwork(t.Context(), 400, "US"); image != "" || err == nil {
		t.Fatalf("cold outage: %q, %v", image, err)
	}

	if image, err := client.Artwork(t.Context(), 400, "US"); image != "" || err != nil || requests.Load() != 1 {
		t.Fatalf("cold outage was not cached: %q, %v, requests=%d", image, err, requests.Load())
	}

	for _, tc := range []struct {
		mode int64
		want string
	}{
		{
			mode: 1,
			want: "https://example.com/capsule.jpg",
		},
		{
			mode: 2,
			want: "https://example.com/capsule.jpg",
		},
		{
			mode: 3,
			want: "https://example.com/new/header.jpg?t=456",
		},
	} {
		mode.Store(tc.mode)
		expireArtwork(client, "400:US")
		image, err := client.Artwork(t.Context(), 400, "US")
		if err != nil || image != tc.want {
			t.Fatalf("refresh mode %d: %q, %v", tc.mode, image, err)
		}
	}
}

func TestArtworkCacheBoundAndCountryIsolation(t *testing.T) {
	cfg := testutil.Config(t)
	cfg.ArtworkCacheMaximumEntries = 2
	client := New(cfg)
	client.rememberArtwork("400:US", "https://example.com/us.jpg")
	client.rememberArtwork("400:DE", "https://example.com/de.jpg")
	client.mu.Lock()
	entry := client.artwork["400:DE"]
	entry.lastUsed = time.Now().Add(-time.Hour)
	client.artwork["400:DE"] = entry
	client.mu.Unlock()
	client.rememberArtwork("500:US", "https://example.com/500.jpg")
	if image, _ := client.cachedArtwork("400:US"); image != "https://example.com/us.jpg" {
		t.Fatalf("country-specific artwork overwritten: %q", image)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if _, exists := client.artwork["400:DE"]; exists || len(client.artwork) != 2 {
		t.Fatal("artwork cache did not evict its least recently used entry")
	}
}

func TestArtworkRefreshSurvivesCallerCancellation(t *testing.T) {
	var requests atomic.Int64
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `{"400":{"success":true,"data":{"steam_appid":400,"name":"Portal","header_image":"https://example.com/header.jpg"}}}`)
	}))
	defer server.Close()
	defer close(release)
	client := New(testutil.Config(t))
	client.DetailsURL = server.URL
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := client.Artwork(ctx, 400, "US")
		finished <- err
	}()
	<-started
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation: %v", err)
	}

	release <- struct{}{}
	image, err := client.Artwork(t.Context(), 400, "US")
	if err != nil || image != "https://example.com/header.jpg" || requests.Load() != 1 {
		t.Fatalf("shared refresh was canceled or duplicated: %q, %v, requests=%d", image, err, requests.Load())
	}
}
