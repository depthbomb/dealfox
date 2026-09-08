package steam

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/depthbomb/dealfox/internal/domain"
)

func (c *Client) Catalog(ctx context.Context, consume func(context.Context, []domain.App) error) error {
	if c.cfg.SteamAPIKey == nil {
		return errors.New("STEAM_WEB_API_KEY is required")
	}

	for _, kind := range []string{"game", "dlc"} {
		var cursor int64
		for {
			input := map[string]any{
				"include_games":    kind == "game",
				"include_dlc":      kind == "dlc",
				"include_hardware": false,
				"include_software": false,
				"include_videos":   false,
				"last_appid":       cursor,
				"max_results":      50000,
			}
			encoded, err := json.Marshal(input)
			if err != nil {
				return err
			}

			body, err := c.request(ctx, c.CatalogURL, url.Values{
				"input_json": {string(encoded)},
			}, c.cfg.SteamAPIKey.Release())
			if err != nil {
				return err
			}

			var page struct {
				Response *struct {
					Apps []struct {
						ID       int64  `json:"appid"`
						Name     string `json:"name"`
						Modified int64  `json:"last_modified"`
						Changed  int64  `json:"price_change_number"`
					} `json:"apps"`
					More bool  `json:"have_more_results"`
					Last int64 `json:"last_appid"`
				} `json:"response"`
			}
			if json.Unmarshal(body, &page) != nil || page.Response == nil || page.Response.Apps == nil {
				return errors.New("steam returned invalid catalog data")
			}

			apps := make([]domain.App, 0, len(page.Response.Apps))
			for _, a := range page.Response.Apps {
				if a.ID > 0 && strings.TrimSpace(a.Name) != "" {
					apps = append(apps, domain.App{
						ID:                a.ID,
						Name:              strings.TrimSpace(a.Name),
						Type:              kind,
						LastModified:      a.Modified,
						PriceChangeNumber: a.Changed,
					})
				}
			}

			if err := consume(ctx, apps); err != nil {
				return err
			}

			if !page.Response.More {
				break
			}

			if page.Response.Last <= cursor {
				return errors.New("steam catalog cursor did not advance")
			}

			cursor = page.Response.Last
		}
	}

	return nil
}
