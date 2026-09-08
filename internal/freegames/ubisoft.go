package freegames

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"golang.org/x/net/html"
)

var usdPrice = regexp.MustCompile(`\$([0-9]+\.[0-9]{2})`)

func classText(tile *html.Node, class string) string {
	for _, n := range nodes(tile, func(n *html.Node) bool {
		return hasClass(n, class)
	}) {
		return textContent(n)
	}

	return ""
}

func ubisoftTile(tile *html.Node, proof Evidence, now time.Time) (Offer, bool) {
	content := strings.ToLower(textContent(tile))
	badge := nodes(tile, func(n *html.Node) bool {
		return hasClass(n, "giveaway") || hasClass(n, "giveaway-badge") || strings.EqualFold(textContent(n), "giveaway")
	})
	if len(badge) == 0 || excluded(content) {
		return Offer{}, false
	}

	o := Offer{
		Source:        "ubisoft",
		ProductID:     attr(tile, "data-itemid"),
		Country:       "US",
		Platform:      "PC",
		Title:         classText(tile, "prod-title"),
		ObservedAt:    now,
		ParserVersion: 1,
		Evidence:      []Evidence{proof},
		Reason:        "retail giveaway requires exact-edition price, library acquisition, and description confirmation",
	}
	for _, link := range nodes(tile, func(n *html.Node) bool {
		return n.Data == "a" && hasClass(n, "thumb-link")
	}) {
		o.URL = resolve("https://store.ubisoft.com/us/free-games", attr(link, "href"))
		break
	}
	for _, img := range nodes(tile, func(n *html.Node) bool {
		return n.Data == "img"
	}) {
		o.ImageURL = resolve(o.URL, attr(img, "data-src"))
		if allowedURL("ubisoft", o.ImageURL, true) {
			break
		}
	}
	for _, n := range nodes(tile, func(n *html.Node) bool {
		return attr(n, "data-enddate") != "" || attr(n, "data-end-date") != ""
	}) {
		value := attr(n, "data-enddate")
		if value == "" {
			value = attr(n, "data-end-date")
		}
		o.EndsAt, _ = time.Parse(time.RFC3339, value)
		break
	}
	o.Campaign = o.ProductID
	if !o.EndsAt.IsZero() {
		o.Campaign += ":" + o.EndsAt.UTC().Format(time.RFC3339)
	}

	original := usdPrice.FindStringSubmatch(classText(tile, "price-standard"))
	final := usdPrice.FindStringSubmatch(classText(tile, "price-sales"))
	if len(original) != 2 || len(final) != 2 {
		return o, true
	}

	paid, err := domain.ParseBudget(original[1] + " USD")
	game := false
	for _, n := range nodes(tile, func(n *html.Node) bool {
		return attr(n, "data-tc100") != ""
	}) {
		data, _ := decode([]byte(attr(n, "data-tc100")))
		if str(data, "pid") == o.ProductID && str(data, "productType") == "games" && str(data, "platform") == "pcdl" {
			game = true
		}
	}
	o.Eligible = err == nil && paid.Minor > 0 && final[1] == "0.00" && game && strings.Contains(content, "add to my games")
	if o.Eligible {
		o.Reason = "persistent library acquisition inferred from exact normally-paid PC game's giveaway badge, zero price, and ADD TO MY GAMES action"
	}

	return o, true
}

func ubisoftEmbedded(body []byte) []any {
	state := scriptJSON(body, "window.__PRELOADED_STATE__")
	var entries []any
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case object:
			for key, child := range value {
				if key == "NewsPersonalization" {
					if slots, ok := child.(map[string]any); ok {
						for _, slot := range slots {
							if str(slot, "status") == "SUCCESS" {
								entries = append(entries, list(slot, "data", "news")...)
							}
						}
					}
				}

				if strings.HasPrefix(key, "NewsPersonalization.") && str(child, "status") == "SUCCESS" {
					entries = append(entries, list(child, "data", "news")...)
				}
				visit(child)
			}
		case map[string]any:
			visit(object(value))
		case []any:
			for _, child := range value {
				visit(child)
			}
		}
	}
	visit(state)

	return entries
}

func (c *Client) ubisoft(ctx context.Context) Result {
	result := Result{}
	storeURL := "https://store.ubisoft.com/us/free-games?lang=en_US"
	body, proof, err := c.get(ctx, "ubisoft", storeURL, 0)
	tiles := nodes(document(body), func(n *html.Node) bool {
		return hasClass(n, "product-tile")
	})
	if err != nil || len(tiles) == 0 {
		result.Problems = append(result.Problems, "Ubisoft retail tiles unavailable or changed")
	}
	for _, tile := range tiles {
		o, candidate := ubisoftTile(tile, proof, c.clock())
		if !candidate {
			continue
		}

		data, detailProof, err := c.get(ctx, "ubisoft", o.URL, time.Hour)
		doc := document(data)
		o.Description = metadata(doc, "description")
		o.Evidence = append(o.Evidence, detailProof)
		if err != nil || strings.Contains(strings.ToLower(o.Description), "ubisoft store") || !o.Publishable(c.clock()) {
			o.Eligible = false
			o.Reason += "; useful exact-game description or metadata missing"
		}
		result.Offers = append(result.Offers, o)
	}

	body, feedProof, err := c.get(ctx, "ubisoft", "https://public-ubiservices.ubi.com/v1/spaces/news?spaceId=6d0af36b-8226-44b6-a03b-4660073a6349", 0)
	feed, parseErr := decode(body)
	entries := list(feed, "news")
	if err != nil || parseErr != nil || node(feed, "news") == nil {
		body, feedProof, err = c.get(ctx, "ubisoft", "https://www.ubisoft.com/en-us/games/free", 0)
		entries = ubisoftEmbedded(body)
		if err != nil || len(entries) == 0 {
			result.Problems = append(result.Problems, "Ubisoft Free Events and HTML fallback unavailable")
		}
	}

	campaigns := make(map[string]Evidence)
	for _, entry := range entries {
		kind := strings.ToLower(str(entry, "type"))
		if str(entry, "placement") != "freeevents" || kind == "free2play" || kind == "gametrial" || excluded(kind+" "+str(entry, "title")) {
			continue
		}

		for _, link := range list(entry, "links") {
			raw := str(link, "param")
			if allowedURL("ubisoft", raw, false) {
				campaigns[raw] = feedProof
			}
		}
	}

	newsURL := "https://news.ubisoft.com/en-us/"
	body, newsProof, err := c.get(ctx, "ubisoft", newsURL, 24*time.Hour)
	if err != nil {
		result.Problems = append(result.Problems, "Ubisoft editorial discovery unavailable")
	} else {
		articles := make(map[string]bool)
		for _, link := range nodes(document(body), func(n *html.Node) bool {
			return n.Data == "a"
		}) {
			raw := resolve(newsURL, attr(link, "href"))
			if !strings.HasPrefix(raw, "https://news.ubisoft.com/en-us/article/") || articles[raw] || len(articles) >= 2 {
				continue
			}
			articles[raw] = true
			data, articleProof, err := c.get(ctx, "ubisoft", raw, 24*time.Hour)
			if err != nil {
				continue
			}

			for _, link := range nodes(document(data), func(n *html.Node) bool {
				return n.Data == "a"
			}) {
				target := resolve(raw, attr(link, "href"))
				if strings.HasPrefix(target, "https://register.ubisoft.com/") {
					campaigns[target] = articleProof
				}
			}
		}
		_ = newsProof
	}

	count := 0
	for raw, origin := range campaigns {
		if count >= 5 {
			result.Problems = append(result.Problems, "Ubisoft campaign request budget exceeded")
			break
		}
		count++
		data, campaignProof, err := c.get(ctx, "ubisoft", raw, 6*time.Hour)
		if err != nil {
			result.Problems = append(result.Problems, "Ubisoft linked campaign unavailable")
			continue
		}

		config := scriptJSON(data, "window.SiteGen.SITEGEN_CONFIG")
		doc := document(data)
		title := metadata(doc, "og:title")
		if excluded(title) || excluded(str(config, "type")) {
			continue
		}

		u, _ := url.Parse(raw)
		result.Offers = append(result.Offers, Offer{
			Source:        "ubisoft",
			ProductID:     strings.Trim(u.Path, "/"),
			Campaign:      raw,
			Country:       "US",
			Platform:      "unconfirmed",
			Title:         title,
			Description:   metadata(doc, "description"),
			ImageURL:      metadata(doc, "og:image"),
			URL:           raw,
			ObservedAt:    c.clock(),
			ParserVersion: 1,
			Reason:        "linked campaign quarantined: request schema does not establish exact-edition paid, active full-game, and persistent entitlement evidence",
			Evidence:      []Evidence{origin, campaignProof},
		})
	}

	return result
}
