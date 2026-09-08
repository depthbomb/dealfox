package freegames

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var steamAppPath = regexp.MustCompile(`/app/([0-9]+)(?:/|$)`)
var steamNewsPath = regexp.MustCompile(`/games/([0-9]+)/announcements/detail/([0-9]+)`)

func steamAnnouncement(body []byte, raw string) int64 {
	match := steamNewsPath.FindStringSubmatch(raw)
	if len(match) != 3 {
		return 0
	}

	for _, n := range nodes(document(body), func(n *html.Node) bool {
		return attr(n, "data-partnereventstore") != ""
	}) {
		decoder := json.NewDecoder(strings.NewReader(attr(n, "data-partnereventstore")))
		decoder.UseNumber()
		var events []any
		if decoder.Decode(&events) != nil {
			continue
		}
		for _, event := range events {
			if str(event, "appid") == match[1] && str(event, "announcement_body", "gid") == match[2] {
				id, _ := strconv.ParseInt(match[1], 10, 64)

				return id
			}
		}
	}

	return 0
}

func steamOption(option any) bool {
	original, originalOK := integer(option, "original_price_in_cents")
	final, finalOK := integer(option, "final_price_in_cents")
	packageID, packageOK := integer(option, "packageid")
	count, countOK := integer(option, "included_game_count")

	return flag(option, "is_free_to_keep") && originalOK && original > 0 && finalOK && final == 0 && packageOK && packageID > 0 && node(option, "bundleid") == nil && (!countOK || count == 1) && !excluded(str(option, "purchase_option_name"))
}

func (c *Client) steamNews(ctx context.Context, result *Result) []int64 {
	var ids []int64
	next := "https://store.steampowered.com/news/search/?l=english&term=free+to+keep&feed=steam_community_announcements"
	budget := 5
	for range 2 {
		body, _, err := c.get(ctx, "steam", next, 6*time.Hour)
		if err != nil {
			result.Problems = append(result.Problems, "Steam publisher-news discovery unavailable")
			break
		}

		doc := document(body)
		for _, block := range nodes(doc, func(n *html.Node) bool {
			return hasClass(n, "newsPostBlock")
		}) {
			for _, title := range nodes(block, func(n *html.Node) bool {
				return hasClass(n, "posttitle")
			}) {
				for _, link := range nodes(title, func(n *html.Node) bool {
					return n.Data == "a"
				}) {
					raw := resolve(next, attr(link, "href"))
					u, _ := url.Parse(raw)
					publisher := u != nil && (strings.HasPrefix(u.Path, "/news/externalpost/steam_community_announcements/") || steamNewsPath.MatchString(u.Path))
					if !publisher || !allowedURL("steam", raw, false) || budget == 0 {
						continue
					}
					budget--
					article, proof, err := c.get(ctx, "steam", raw, 24*time.Hour)
					if err != nil {
						result.Problems = append(result.Problems, "Steam publisher announcement unavailable")
						continue
					}

					if id := steamAnnouncement(article, proof.URL); id > 0 {
						ids = append(ids, id)
					}
				}
			}
		}

		var target string
		for _, link := range nodes(doc, func(n *html.Node) bool {
			return n.Data == "a" && attr(n, "id") == "more_posts_url"
		}) {
			target = attr(link, "href")
		}
		if target == "" {
			break
		}
		next = resolve(next, target)
		u, err := url.Parse(next)
		if err != nil || u.Host != "store.steampowered.com" || !strings.HasPrefix(u.Path, "/news/") {
			break
		}
		q := u.Query()
		q.Set("l", "english")
		q.Set("feed", "steam_community_announcements")
		u.RawQuery = q.Encode()
		next = u.String()
	}

	return ids
}

func (c *Client) steam(ctx context.Context) Result {
	result := Result{}
	var ids []int64
	for page := range 3 {
		raw := fmt.Sprintf("https://store.steampowered.com/search/results?maxprice=free&specials=1&category1=998&hidef2p=1&cc=US&l=english&json=1&start=%d&count=50&infinite=1", page*50)
		body, _, err := c.get(ctx, "steam", raw, 0)
		data, parseErr := decode(body)
		total, ok := integer(data, "total_count")
		fragment, fragmentOK := node(data, "results_html").(string)
		legacy, legacyOK := node(data, "items").([]any)
		if err == nil && parseErr == nil && legacyOK && len(legacy) == 0 && node(data, "desc") != nil {
			break
		}

		if err != nil || parseErr != nil || !ok || !fragmentOK || (!flag(data, "success") && str(data, "success") != "1") {
			result.Problems = append(result.Problems, "Steam free-specials search unavailable or changed")
			break
		}

		before := len(ids)
		for _, link := range nodes(document([]byte(fragment)), func(n *html.Node) bool {
			return n.Data == "a"
		}) {
			match := steamAppPath.FindStringSubmatch(attr(link, "href"))
			if len(match) == 2 {
				id, _ := strconv.ParseInt(match[1], 10, 64)
				ids = append(ids, id)
			}
		}
		if len(ids) == before && total > int64(page*50) {
			result.Problems = append(result.Problems, "Steam search page has no readable AppIDs")
			break
		}
		if total <= int64((page+1)*50) {
			break
		}

		if page == 2 {
			result.Problems = append(result.Problems, "Steam search exceeded 150 candidates")
		}
	}
	ids = append(ids, c.steamNews(ctx, &result)...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	enrichments := 0
	for offset := 0; offset < len(ids); offset += 25 {
		batch := ids[offset:min(offset+25, len(ids))]
		var requests []object
		for _, id := range batch {
			requests = append(requests, object{
				"appid": id,
			})
		}
		input, _ := json.Marshal(object{
			"ids": requests,
			"context": object{
				"language":     "english",
				"country_code": "US",
				"steam_realm":  1,
			},
			"data_request": object{
				"include_assets":               true,
				"include_basic_info":           true,
				"include_all_purchase_options": true,
			},
		})
		raw := "https://api.steampowered.com/IStoreBrowseService/GetItems/v1/?input_json=" + url.QueryEscape(string(input))
		body, proof, err := c.get(ctx, "steam", raw, 2*time.Minute)
		data, parseErr := decode(body)
		items := list(data, "response", "store_items")
		if err != nil || parseErr != nil || len(items) != len(batch) {
			result.Problems = append(result.Problems, "Steam StoreBrowse confirmation incomplete")
			continue
		}

		for _, item := range items {
			id, ok := integer(item, "appid")
			if !ok || !slices.Contains(batch, id) || str(item, "success") != "1" || str(item, "type") != "0" {
				continue
			}

			options := append(list(item, "purchase_options"), node(item, "best_purchase_option"))
			for _, option := range options {
				if !steamOption(option) {
					continue
				}

				o := Offer{
					Source:        "steam",
					ProductID:     strconv.FormatInt(id, 10),
					Campaign:      str(option, "packageid"),
					Country:       "US",
					Platform:      "PC",
					Title:         str(item, "name"),
					URL:           fmt.Sprintf("https://store.steampowered.com/app/%d/", id),
					ObservedAt:    c.clock(),
					ParserVersion: 1,
					Reason:        "explicit Free to Keep package with positive original cents and zero final cents",
					Evidence: []Evidence{recordEvidence(proof, "explicit free-to-keep purchase option for this AppID", object{
						"appid":  id,
						"option": option,
					})},
				}
				for _, discount := range list(option, "active_discounts") {
					seconds, ok := integer(discount, "discount_end_date")
					if ok && seconds > 0 {
						o.EndsAt = time.Unix(seconds, 0).UTC()
						o.Campaign += ":" + str(discount, "discount_end_date")
						break
					}
				}
				if enrichments >= 10 {
					o.Reason = "Steam metadata budget exhausted"
					result.Offers = append(result.Offers, o)
					result.Problems = append(result.Problems, o.Reason)
					break
				}
				enrichments++
				body, metadataProof, err := c.get(ctx, "steam", fmt.Sprintf("https://store.steampowered.com/api/appdetails?appids=%d&cc=us&l=english&filters=basic", id), 24*time.Hour)
				detail, parseErr := decode(body)
				metadata := node(detail, o.ProductID, "data")
				o.Description = plain(str(metadata, "short_description"))
				o.ImageURL = str(metadata, "header_image")
				o.Evidence = append(o.Evidence, recordEvidence(metadataProof, "exact AppID full-game classification and presentation", metadata))
				o.Eligible = err == nil && parseErr == nil && flag(detail, o.ProductID, "success") && str(metadata, "type") == "game" && str(metadata, "steam_appid") == o.ProductID && !excluded(o.Title)
				if !o.Publishable(c.clock()) {
					o.Eligible = false
					o.Reason = "Steam exact game classification or presentation unavailable"
				}
				result.Offers = append(result.Offers, o)
				break
			}
		}
	}

	return result
}
