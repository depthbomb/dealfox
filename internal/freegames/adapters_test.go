package freegames

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}

	return body
}

func fixtureClient(t *testing.T, route func(*http.Request) []byte) *Client {
	t.Helper()
	client := NewClient()
	client.clock = func() time.Time {
		return time.Date(2026, 9, 7, 19, 0, 0, 0, time.UTC)
	}
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := route(r)
		status := 200
		if body == nil {
			status = 503
		}

		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    r,
		}, nil
	})

	return client
}

func TestGOGPositiveCatalogPriceGiveaway(t *testing.T) {
	client := fixtureClient(t, func(r *http.Request) []byte {
		switch {
		case strings.Contains(r.URL.Path, "/sections/"):
			return fixture(t, "gog_section.json")
		case r.URL.Host == "sections.gog.com":
			return []byte(`{"sections":[{"sectionType":"GIVEAWAY_SECTION","sectionId":"test"}]}`)
		case r.URL.Host == "api.gog.com":
			return fixture(t, "gog_product.json")
		case r.URL.Host == "www.gog.com":
			return fixture(t, "gog_product.html")
		}

		return nil
	})
	result := client.Scan(t.Context(), "gog")
	if len(result.Problems) != 0 || len(result.Offers) != 1 || !result.Offers[0].Publishable(client.clock()) {
		t.Fatalf("saved positive-price dedicated giveaway rejected: %+v", result)
	}
	if result.Offers[0].Publishable(result.Offers[0].EndsAt) {
		t.Fatal("expired giveaway accepted")
	}

	if gogMarker(fixture(t, "gog_product.html"), "wrong-product") {
		t.Fatal("unrelated product marker accepted")
	}
	section, _ := decode(fixture(t, "gog_section.json"))
	product := node(section, "properties", "product").(map[string]any)
	price := node(product, "price", "baseMoney").(map[string]any)
	for _, amount := range []any{"0.00", "NaN", nil, true, "-1.00"} {
		price["amount"] = amount
		if gogPaidGame(product) {
			t.Fatalf("invalid paid proof accepted: %v", amount)
		}
	}
}

func TestEpicCatalogFindsOfferAbsentFromWeekly(t *testing.T) {
	client := fixtureClient(t, func(r *http.Request) []byte {
		if strings.Contains(r.URL.Path, "freeGamesPromotions") {
			return fixture(t, "epic_weekly.json")
		}

		if strings.Contains(r.URL.Query().Get("query"), "catalogOffer(") {
			return fixture(t, "epic_detail.json")
		}

		return fixture(t, "epic_catalog.json")
	})
	result := client.Scan(t.Context(), "epic")
	if len(result.Problems) != 0 {
		t.Fatalf("unexpected discovery failure: %v", result.Problems)
	}
	for _, offer := range result.Offers {
		if offer.Title == "NoRush!" {
			if !offer.Publishable(client.clock()) {
				t.Fatalf("catalog-only promotion rejected: %+v", offer)
			}

			return
		}
	}
	t.Fatalf("catalog-only NoRush! missing: %+v", result)
}

func TestEpicRejectsRestrictedAndMismatchedOffers(t *testing.T) {
	catalog, _ := decode(fixture(t, "epic_catalog.json"))
	detail, _ := decode(fixture(t, "epic_detail.json"))
	item := list(catalog, "data", "Catalog", "searchStore", "elements")[0]
	for _, candidate := range list(catalog, "data", "Catalog", "searchStore", "elements") {
		if str(candidate, "title") == "NoRush!" {
			item = candidate
		}
	}
	now := time.Date(2026, 9, 7, 19, 0, 0, 0, time.UTC)
	_, end := epicWindow(item, now)
	original := node(detail, "data", "Catalog", "catalogOffer")
	for _, mutation := range []struct {
		key   string
		value any
	}{
		{
			key:   "isCodeRedemptionOnly",
			value: true,
		},
		{
			key:   "offerType",
			value: "ADD_ON",
		},
		{
			key:   "id",
			value: "other",
		},
		{
			key:   "countriesBlacklist",
			value: []any{"US"},
		},
	} {
		encoded, _ := json.Marshal(original)
		changed, _ := decode(encoded)
		changed[mutation.key] = mutation.value
		if epicDetailEligible(changed, item, end) {
			t.Fatalf("accepted %s=%v", mutation.key, mutation.value)
		}
	}
}

func TestSteamExactFreeToKeepOption(t *testing.T) {
	base := `{"packageid":123,"is_free_to_keep":true,"original_price_in_cents":"1999","final_price_in_cents":"0","included_game_count":1}`
	option, _ := decode([]byte(base))
	if !steamOption(option) {
		t.Fatal("valid digit-string cents rejected")
	}
	for _, mutation := range []string{
		strings.Replace(base, `"is_free_to_keep":true`, `"is_free_to_keep":false`, 1),
		strings.Replace(base, `"1999"`, `"0"`, 1),
		strings.Replace(base, `"1999"`, `true`, 1),
		strings.Replace(base, `"final_price_in_cents":"0"`, `"final_price_in_cents":null`, 1),
		strings.Replace(base, `"packageid":123`, `"packageid":123,"bundleid":456`, 1),
		strings.Replace(base, `"included_game_count":1`, `"included_game_count":2`, 1),
	} {
		value, _ := decode([]byte(mutation))
		if steamOption(value) {
			t.Fatalf("unsafe option accepted: %s", mutation)
		}
	}
}

func TestUbisoftNegativeTilesAndHTMLFallback(t *testing.T) {
	count := 0
	for _, tile := range nodes(document(fixture(t, "ubisoft_store.html")), func(n *html.Node) bool {
		return hasClass(n, "product-tile")
	}) {
		count++
		if _, candidate := ubisoftTile(tile, Evidence{}, time.Now()); candidate {
			t.Fatal("non-giveaway tile or global giveaway string accepted")
		}
	}
	if count != 9 {
		t.Fatalf("tile parser drift: %d", count)
	}

	entries := ubisoftEmbedded(fixture(t, "ubisoft_free.html"))
	ids := make(map[string]bool)
	for _, entry := range entries {
		ids[str(entry, "newsId")] = true
	}
	if len(ids) != 24 {
		t.Fatalf("embedded feed parser drift: %d IDs", len(ids))
	}
}

func TestTransportBackoffAndBoundaries(t *testing.T) {
	requests := 0
	client := fixtureClient(t, func(*http.Request) []byte {
		requests++

		return nil
	})
	for range 2 {
		_, _, err := client.get(context.Background(), "epic", "https://store.epicgames.com/graphql", 0)
		if err == nil {
			t.Fatal("failed request accepted")
		}
	}
	if requests != 1 {
		t.Fatal("source backoff ignored")
	}

	for _, raw := range []string{"https://luna.amazon.com/game", "https://epicgames.com.attacker.test/", "http://store.epicgames.com/", "https://user:pass@store.epicgames.com/"} {
		if allowedURL("epic", raw, false) {
			t.Fatalf("unapproved URL accepted: %s", raw)
		}
	}
}

func TestSteamNewsRequiresMatchingAnnouncementAndAppID(t *testing.T) {
	body := fixture(t, "steam_announcement.html")
	if id := steamAnnouncement(body, "https://steamcommunity.com/games/1272160/announcements/detail/697644549761140171"); id != 1272160 {
		t.Fatalf("publisher announcement identity not recovered: %d", id)
	}

	for _, raw := range []string{
		"https://steamcommunity.com/games/999/announcements/detail/697644549761140171",
		"https://steamcommunity.com/games/1272160/announcements/detail/999",
	} {
		if steamAnnouncement(body, raw) != 0 {
			t.Fatal("mismatched publisher announcement accepted")
		}
	}
}

func TestGOGHomepageFallbackPreservesDedicatedGiveaway(t *testing.T) {
	client := fixtureClient(t, func(r *http.Request) []byte {
		switch {
		case r.URL.Host == "sections.gog.com":
			return nil
		case r.URL.Host == "api.gog.com":
			return fixture(t, "gog_product.json")
		case strings.Contains(r.URL.Path, "/game/"):
			return fixture(t, "gog_product.html")
		default:
			return fixture(t, "gog_home.html")
		}
	})
	result := client.Scan(t.Context(), "gog")
	if len(result.Offers) != 1 || !result.Offers[0].Publishable(client.clock()) || len(result.Problems) == 0 {
		t.Fatalf("fallback lost offer or concealed primary failure: %+v", result)
	}
}

func TestSteamDiscoveryConfirmationAndNonGameRejection(t *testing.T) {
	for _, kind := range []string{"game", "music", "dlc"} {
		client := fixtureClient(t, func(r *http.Request) []byte {
			switch {
			case strings.Contains(r.URL.Path, "/search/results"):
				return []byte(`{"success":1,"total_count":1,"results_html":"<a href='https://store.steampowered.com/app/123/'></a>"}`)
			case strings.Contains(r.URL.Path, "/news/"):
				return []byte(`<html><div id="news">No results</div></html>`)
			case strings.Contains(r.URL.Path, "/GetItems/"):
				return []byte(`{"response":{"store_items":[{"appid":123,"success":1,"type":0,"name":"Game","best_purchase_option":{"bundleid":99,"is_free_to_keep":true,"original_price_in_cents":"999","final_price_in_cents":"0"},"purchase_options":[{"packageid":456,"included_game_count":1,"is_free_to_keep":true,"original_price_in_cents":"1999","final_price_in_cents":"0"}]}]}}`)
			case strings.Contains(r.URL.Path, "/appdetails"):
				return []byte(`{"123":{"success":true,"data":{"steam_appid":123,"type":"` + kind + `","short_description":"A complete game","header_image":"https://shared.akamai.steamstatic.com/header.jpg"}}}`)
			}

			return nil
		})
		result := client.Scan(t.Context(), "steam")
		if len(result.Problems) != 0 || len(result.Offers) != 1 || result.Offers[0].Publishable(client.clock()) != (kind == "game") || result.Offers[0].Campaign != "456" {
			t.Fatalf("%s: exact package/game gate failed: %+v", kind, result)
		}
	}
}
