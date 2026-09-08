package freegames

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

//go:embed queries/epic_catalog.graphql
var epicCatalogQuery string

//go:embed queries/epic_detail.graphql
var epicDetailQuery string

func epicQuery(query string, variables object) string {
	encoded, _ := json.Marshal(variables)

	return "https://store.epicgames.com/graphql?query=" + url.QueryEscape(query) + "&variables=" + url.QueryEscape(string(encoded))
}

func epicWindow(item any, now time.Time) (time.Time, time.Time) {
	for _, group := range list(item, "promotions", "promotionalOffers") {
		for _, promotion := range list(group, "promotionalOffers") {
			start := instant(promotion, "startDate")
			end := instant(promotion, "endDate")
			if !start.IsZero() && !end.IsZero() && !now.Before(start) && now.Before(end) {
				return start, end
			}
		}
	}

	return time.Time{}, time.Time{}
}

func epicBase(item any) bool {
	original, originalOK := integer(item, "price", "totalPrice", "originalPrice")
	final, finalOK := integer(item, "price", "totalPrice", "discountPrice")
	if str(item, "offerType") != "BASE_GAME" || !originalOK || original <= 0 || !finalOK || final != 0 || str(item, "price", "totalPrice", "currencyCode") != "USD" || excluded(str(item, "title")) {
		return false
	}

	categories := list(item, "categories")
	base := false
	for _, category := range categories {
		path := str(category, "path")
		if excluded(path) || strings.Contains(path, "bundles") {
			return false
		}

		if path == "games" || path == "games/edition/base" || path == "games/edition" {
			base = true
		}
	}

	return base
}

func epicDetailEligible(detail any, item any, end time.Time) bool {
	if !epicBase(detail) || str(detail, "id") != str(item, "id") || str(detail, "namespace") != str(item, "namespace") || str(detail, "status") != "ACTIVE" || node(detail, "isCodeRedemptionOnly") != false || (node(detail, "prePurchase") != nil && node(detail, "prePurchase") != false) {
		return false
	}

	if expires := instant(detail, "expiryDate"); !expires.IsZero() && expires.Before(end) {
		return false
	}

	for _, country := range list(detail, "countriesBlacklist") {
		if country == "US" {
			return false
		}
	}
	whitelist := list(detail, "countriesWhitelist")
	if len(whitelist) > 0 && !slices.Contains(whitelist, any("US")) {
		return false
	}

	for _, attribute := range list(detail, "customAttributes") {
		if excluded(str(attribute, "key") + " " + str(attribute, "value")) {
			return false
		}
	}

	for _, line := range list(detail, "price", "lineOffers") {
		for _, rule := range list(line, "appliedRules") {
			if instant(rule, "endDate").Equal(end) {
				return true
			}
		}
	}

	return false
}

func (c *Client) epic(ctx context.Context) Result {
	result := Result{}
	seen := make(map[string]bool)
	details := 0
	consume := func(items []any, evidence Evidence, weekly bool) {
		for _, item := range items {
			start, end := epicWindow(item, c.clock())
			if end.IsZero() || !epicBase(item) {
				continue
			}

			o := Offer{
				Source:        "epic",
				ProductID:     str(item, "namespace") + ":" + str(item, "id"),
				Campaign:      start.UTC().Format(time.RFC3339),
				Country:       "US",
				Platform:      "PC",
				Title:         str(item, "title"),
				Description:   plain(str(item, "description")),
				StartsAt:      start,
				EndsAt:        end,
				ObservedAt:    c.clock(),
				ParserVersion: 1,
				Eligible:      weekly,
				Reason:        "active weekly promotion of a normally-paid base game",
				Evidence:      []Evidence{recordEvidence(evidence, "exact base-game price and promotion interval", item)},
			}
			if seen[o.Key()] {
				continue
			}

			for _, mapping := range append(list(item, "offerMappings"), list(item, "catalogNs", "mappings")...) {
				slug := str(mapping, "pageSlug")
				if str(mapping, "pageType") == "productHome" && slug != "" && !strings.ContainsAny(slug, "?#:") {
					o.URL = "https://store.epicgames.com/en-US/p/" + strings.TrimPrefix(slug, "/")
					break
				}
			}
			for _, img := range list(item, "keyImages") {
				if allowedURL("epic", str(img, "url"), true) {
					o.ImageURL = str(img, "url")
					break
				}
			}

			if !weekly {
				if details >= 10 {
					o.Eligible = false
					o.Reason = "catalog detail budget exhausted"
					result.Problems = append(result.Problems, o.Reason)
					result.Offers = append(result.Offers, o)
					continue
				}
				details++
				raw := epicQuery(epicDetailQuery, object{
					"sandboxId": str(item, "namespace"),
					"offerId":   str(item, "id"),
					"locale":    "en-US",
					"country":   "US",
				})
				body, proof, err := c.get(ctx, "epic", raw, time.Hour)
				detail, decodeErr := decode(body)
				o.Eligible = err == nil && decodeErr == nil && len(list(detail, "errors")) == 0 && epicDetailEligible(node(detail, "data", "Catalog", "catalogOffer"), item, end)
				o.Evidence = append(o.Evidence, recordEvidence(proof, "standard acquisition, region and matching discount end", node(detail, "data", "Catalog", "catalogOffer")))
				o.Reason = "persistent entitlement inferred from active standard base-game acquisition under a temporary 100% discount"
				if !o.Eligible {
					o.Reason = "catalog acquisition, region, or retention evidence incomplete"
				}
			}

			if !o.Publishable(c.clock()) {
				o.Eligible = false
				o.Reason += "; required metadata or eligibility missing"
			}
			seen[o.Key()] = o.Eligible
			result.Offers = append(result.Offers, o)
		}
	}

	weeklyURL := "https://store-site-backend-static.ak.epicgames.com/freeGamesPromotions?locale=en-US&country=US&allowCountries=US"
	body, evidence, err := c.get(ctx, "epic", weeklyURL, 0)
	weekly, parseErr := decode(body)
	weeklyItems, weeklyOK := node(weekly, "data", "Catalog", "searchStore", "elements").([]any)
	if err != nil || parseErr != nil || !weeklyOK {
		result.Problems = append(result.Problems, "Epic weekly discovery unavailable or changed")
	} else {
		consume(weeklyItems, evidence, true)
	}

	catalogIDs := make(map[string]bool)
	for page := range 3 {
		raw := epicQuery(epicCatalogQuery, object{
			"country":        "US",
			"allowCountries": "US",
			"locale":         "en-US",
			"count":          40,
			"start":          page * 40,
			"freeGame":       true,
			"onSale":         true,
		})
		body, evidence, err := c.get(ctx, "epic", raw, 0)
		catalog, parseErr := decode(body)
		total, valid := integer(catalog, "data", "Catalog", "searchStore", "paging", "total")
		items, itemsOK := node(catalog, "data", "Catalog", "searchStore", "elements").([]any)
		if err != nil || parseErr != nil || len(list(catalog, "errors")) != 0 || !valid || !itemsOK || len(items) == 0 && total > int64(page*40) {
			result.Problems = append(result.Problems, "Epic catalog discovery unavailable or changed")
			break
		}
		for _, item := range items {
			id := str(item, "namespace") + ":" + str(item, "id")
			if catalogIDs[id] {
				result.Problems = append(result.Problems, "Epic catalog repeated an offer across pages")
			}
			catalogIDs[id] = true
		}
		consume(items, evidence, false)
		if int64((page+1)*40) >= total {
			break
		}

		if page == 2 {
			result.Problems = append(result.Problems, fmt.Sprintf("Epic catalog exceeded the %d-offer discovery limit", 120))
		}
	}

	return result
}
