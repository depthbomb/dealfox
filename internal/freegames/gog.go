package freegames

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
	"golang.org/x/net/html"
)

func gogMarker(body []byte, id string) bool {
	doc := document(body)
	for _, button := range nodes(doc, func(n *html.Node) bool {
		return attr(n, "main-button-decider") == id
	}) {
		parent := button.Parent
		if parent == nil || !hasClass(parent, "product-actions-body") {
			continue
		}

		badges := nodes(parent, func(n *html.Node) bool {
			return hasClass(n, "product-actions-price__giveaway")
		})
		for _, link := range nodes(button, func(n *html.Node) bool {
			return n.Data == "a" && hasClass(n, "cart-button__state-giveaway")
		}) {
			target, _ := url.Parse(resolve("https://www.gog.com/en/game/", attr(link, "href")))
			if len(badges) > 0 && target != nil && target.Host == "www.gog.com" && target.Fragment == "giveaway" && (target.Path == "/" || target.Path == "/en" || target.Path == "/en/") {
				return true
			}
		}
	}

	return false
}

func gogPaidGame(product any) bool {
	base := str(product, "price", "baseMoney", "amount")
	money, err := domain.ParseBudget(base + " " + str(product, "price", "baseMoney", "currency"))
	if err != nil || money.Minor <= 0 || money.Currency != "USD" || str(product, "productType") != "game" || excluded(str(product, "title")) {
		return false
	}

	for _, tag := range list(product, "tags") {
		slug := str(tag, "slug")
		if slug == "movie" || slug == "freegame" || slug == "demo" || slug == "dlc" {
			return false
		}
	}

	return true
}

func (c *Client) gog(ctx context.Context) Result {
	result := Result{}
	query := "?locale=en-US&countryCode=US&currencyCode=USD"
	body, _, err := c.get(ctx, "gog", "https://sections.gog.com/v1/pages/2f"+query, 0)
	index, parseErr := decode(body)
	var sections []object
	var proofs []Evidence
	indexSections, indexOK := node(index, "sections").([]any)
	if err == nil && parseErr == nil && indexOK {
		for _, section := range indexSections {
			if str(section, "sectionType") != "GIVEAWAY_SECTION" {
				continue
			}

			if len(sections) >= 3 {
				result.Problems = append(result.Problems, "GOG giveaway section budget exceeded")
				break
			}

			id := str(section, "sectionId")
			if id == "" {
				result.Problems = append(result.Problems, "GOG section ID missing")
				continue
			}
			data, proof, err := c.get(ctx, "gog", "https://sections.gog.com/v1/pages/2f/sections/"+url.PathEscape(id)+query, 0)
			value, parseErr := decode(data)
			if err != nil || parseErr != nil || node(value, "properties") == nil {
				result.Problems = append(result.Problems, "GOG section unavailable or changed")
				continue
			}
			sections = append(sections, value)
			proofs = append(proofs, proof)
		}
	} else {
		result.Problems = append(result.Problems, "GOG section index unavailable or changed")
	}

	if len(result.Problems) > 0 {
		data, proof, err := c.get(ctx, "gog", "https://www.gog.com/en?countryCode=US&currencyCode=USD", 0)
		if err == nil {
			doc := document(data)
			for _, script := range nodes(doc, func(n *html.Node) bool {
				return n.Data == "script" && attr(n, "id") == "gogcom-store-state"
			}) {
				if script.FirstChild == nil {
					continue
				}
				state, _ := decode([]byte(script.FirstChild.Data))
				for key, value := range state {
					u, parseErr := url.Parse("https://" + key)
					if parseErr != nil || u.Host != "sections.gog" || !strings.HasPrefix(u.Path, "/v1/pages/2f/sections/") || u.Query().Get("countryCode") != "US" || u.Query().Get("currencyCode") != "USD" || u.Query().Get("locale") != "en-US" || str(value, "status") != "200" || len(sections) >= 3 {
						continue
					}

					encoded, _ := json.Marshal(node(value, "body"))
					section, _ := decode(encoded)
					if node(section, "properties", "product") == nil || instant(section, "properties", "endDate").IsZero() {
						continue
					}

					matched := false
					for _, banner := range nodes(doc, func(n *html.Node) bool {
						return attr(n, "id") == "giveaway" && attr(n, "data-sid") == strings.TrimPrefix(u.Path, "/v1/pages/2f/sections/")
					}) {
						for _, link := range nodes(banner, func(n *html.Node) bool {
							return hasClass(n, "giveaway__overlay-link")
						}) {
							expected := "https://www.gog.com/en/game/" + str(section, "properties", "product", "slug")
							matched = resolve("https://www.gog.com/en", attr(link, "href")) == expected
						}
					}

					if !matched {
						continue
					}

					sections = append(sections, section)
					proofs = append(proofs, proof)
				}
			}
		}
	}

	for i, section := range sections {
		product := node(section, "properties", "product")
		end := instant(section, "properties", "endDate")
		if product == nil || end.IsZero() {
			result.Problems = append(result.Problems, "GOG giveaway product or end date missing")
			continue
		}

		if !c.clock().Before(end) || !gogPaidGame(product) {
			continue
		}

		o := Offer{
			Source:        "gog",
			ProductID:     str(product, "id"),
			Campaign:      end.UTC().Format(time.RFC3339),
			Country:       "US",
			Platform:      "PC",
			Title:         str(product, "title"),
			ImageURL:      str(product, "coverHorizontal"),
			URL:           "https://www.gog.com/en/game/" + url.PathEscape(str(product, "slug")),
			ClaimURL:      "https://www.gog.com/#giveaway",
			EndsAt:        end,
			ObservedAt:    c.clock(),
			ParserVersion: 1,
			Reason:        "dedicated library giveaway with positive normal price; catalog final price is not claim cost",
			Evidence:      []Evidence{recordEvidence(proofs[i], "dedicated giveaway end and exact normally-paid game", section)},
		}
		body, proof, err := c.get(ctx, "gog", "https://api.gog.com/products/"+url.PathEscape(o.ProductID)+"?expand=description", 24*time.Hour)
		detail, parseErr := decode(body)
		o.Description = plain(str(detail, "description", "lead"))
		o.Evidence = append(o.Evidence, recordEvidence(proof, "exact product classification and description", detail))
		validDetail := err == nil && parseErr == nil && str(detail, "id") == o.ProductID && str(detail, "game_type") == "game" && !excluded(o.Title+" "+o.Description)
		page, proof, pageErr := c.get(ctx, "gog", o.URL+"?countryCode=US&currencyCode=USD", 0)
		o.Evidence = append(o.Evidence, recordEvidence(proof, "scoped purchase block giveaway badge and same-product library claim link", object{
			"product_id":      o.ProductID,
			"giveaway_marker": gogMarker(page, o.ProductID),
		}))
		o.Eligible = validDetail && pageErr == nil && gogMarker(page, o.ProductID)
		if !o.Publishable(c.clock()) {
			o.Eligible = false
			o.Reason = "exact-product giveaway, game classification, or presentation evidence incomplete"
		}
		result.Offers = append(result.Offers, o)
	}

	return result
}
