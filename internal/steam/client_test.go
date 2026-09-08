package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/domain"
	"github.com/depthbomb/dealfox/internal/testutil"
)

func TestInput(t *testing.T) {
	cases := []struct {
		input string
		id    int64
		valid bool
	}{
		{
			input: "400",
			id:    400,
			valid: true,
		},
		{
			input: " https://store.steampowered.com/app/400/Portal/?x=1 ",
			id:    400,
			valid: true,
		},
		{
			input: "Portal",
			valid: true,
		},
		{
			input: "0",
		},
		{
			input: "-10",
		},
		{
			input: "9223372036854775808",
		},
		{
			input: "https://evil.test/app/400",
		},
		{
			input: "https://store.steampowered.com.evil.test/app/400",
		},
		{
			input: "https://store.steampowered.com/bundle/400",
		},
		{
			input: "https://store.steampowered.com/app/nope",
		},
		{
			input: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			id, err := ParseAppID(tc.input)
			if (err == nil) != tc.valid || id != tc.id {
				t.Fatalf("got %d, %v", id, err)
			}
		})
	}
}

func TestPricesFailClosed(t *testing.T) {
	cases := []struct {
		name  string
		json  string
		state string
	}{
		{
			name:  "sale",
			json:  `{"currency":"USD","initial":1000,"final":500,"discount_percent":50}`,
			state: "on_sale",
		},
		{
			name:  "off",
			json:  `{"currency":"USD","initial":1000,"final":1000,"discount_percent":0}`,
			state: "not_on_sale",
		},
		{
			name:  "free to keep",
			json:  `{"currency":"USD","initial":1000,"final":0,"discount_percent":100}`,
			state: "on_sale",
		},
		{
			name:  "incoherent",
			json:  `{"currency":"USD","initial":1000,"final":500,"discount_percent":0}`,
			state: "unknown",
		},
		{
			name:  "negative",
			json:  `{"currency":"USD","initial":1000,"final":-5,"discount_percent":50}`,
			state: "unknown",
		},
		{
			name:  "missing",
			json:  `{"currency":"USD","initial":1000,"discount_percent":50}`,
			state: "unknown",
		},
		{
			name:  "fractional",
			json:  `{"currency":"USD","initial":1000,"final":500.5,"discount_percent":50}`,
			state: "unknown",
		},
		{
			name:  "string money",
			json:  `{"currency":"USD","initial":1000,"final":"500","discount_percent":50}`,
			state: "unknown",
		},
		{
			name:  "empty currency",
			json:  `{"currency":"","initial":1000,"final":500,"discount_percent":50}`,
			state: "unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("cc") != "US" || r.URL.Query().Get("filters") != "price_overview" {
					t.Error("incorrect regional price request")
				}
				fmt.Fprintf(w, `{"10":{"success":true,"data":{"price_overview":%s}}}`, tc.json)
			}))
			defer server.Close()
			client := New(testutil.Config(t))
			client.DetailsURL = server.URL
			prices, err := client.Prices(t.Context(), []int64{10, 11}, "us")
			if err != nil {
				t.Fatal(err)
			}

			if prices[10].SaleState != tc.state || prices[11].Availability != "unknown" {
				t.Fatalf("unexpected prices: %+v", prices)
			}
		})
	}
}

func TestFreeClassificationAndCache(t *testing.T) {
	for _, paid := range []bool{false, true} {
		t.Run(fmt.Sprint(paid), func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Query().Get("filters") == "basic" {
					fmt.Fprint(w, `{"10":{"success":true,"data":{"steam_appid":10,"name":"Game","type":"game","is_free":true,"header_image":"https://shared.akamai.steamstatic.com/store_item_assets/steam/apps/10/version/header.jpg?t=123"}}}`)
				} else if paid {
					fmt.Fprint(w, `{"10":{"success":true,"data":{"price_overview":{"currency":"USD","initial":1000,"final":0,"discount_percent":100}}}}`)
				} else {
					fmt.Fprint(w, `{"10":{"success":true,"data":[]}}`)
				}
			}))
			defer server.Close()
			client := New(testutil.Config(t))
			client.DetailsURL = server.URL
			p, err := client.Details(t.Context(), 10, "US")
			if err != nil {
				t.Fatal(err)
			}

			if (p.Availability == "priced") != paid || (!paid && p.Availability != "free") {
				t.Fatalf("bad free classification: %+v", p)
			}

			if p.CapsuleURL != "https://shared.akamai.steamstatic.com/store_item_assets/steam/apps/10/version/header.jpg?t=123" {
				t.Fatalf("Steam capsule URL was not preserved: %q", p.CapsuleURL)
			}

			if _, err := client.Details(t.Context(), 10, "us"); err != nil || requests.Load() != 2 {
				t.Fatalf("cache miss: %v, requests=%d", err, requests.Load())
			}
		})
	}
}

func TestCircuitAndTimeout(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", 503)
	}))
	defer server.Close()
	cfg := testutil.Config(t)
	cfg.SteamCircuitFailureThreshold = 2
	client := New(cfg)
	client.DetailsURL = server.URL
	for range 4 {
		if _, err := client.Prices(t.Context(), []int64{10}, "US"); err == nil {
			t.Fatal("failed Steam request was accepted")
		}
	}

	if requests.Load() != 2 {
		t.Fatal("open circuit continued making requests")
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	cfg.SteamTimeout = 20 * time.Millisecond
	client = New(cfg)
	client.DetailsURL = slow.URL
	started := time.Now()
	if _, err := client.Prices(t.Context(), []int64{10}, "US"); err == nil || time.Since(started) > time.Second {
		t.Fatal("Steam timeout was not enforced")
	}
}

func TestCatalogPaginationAndKey(t *testing.T) {
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		return "test-secret", key == "STEAM_WEB_API_KEY"
	})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Webapi-Key") != "test-secret" || strings.Contains(r.URL.RawQuery, "test-secret") {
			t.Error("Steam key was missing or exposed in URL")
		}
		var input map[string]any
		if err := json.Unmarshal([]byte(r.URL.Query().Get("input_json")), &input); err != nil {
			t.Error(err)
		}

		if input["last_appid"] == float64(0) {
			fmt.Fprint(w, `{"response":{"apps":[{"appid":10,"name":"Game"}],"have_more_results":true,"last_appid":10}}`)
		} else {
			fmt.Fprint(w, `{"response":{"apps":[{"appid":11,"name":"DLC"}],"have_more_results":false}}`)
		}
	}))
	defer server.Close()
	client := New(&cfg)
	client.CatalogURL = server.URL
	err = client.Catalog(t.Context(), func(_ context.Context, apps []domain.App) error {
		count += len(apps)

		return nil
	})
	if err != nil || count != 4 {
		t.Fatalf("catalog: count=%d, err=%v", count, err)
	}
}
