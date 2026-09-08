package steam

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
)

type overview struct {
	Currency string `json:"currency"`
	Initial  *int64 `json:"initial"`
	Final    *int64 `json:"final"`
	Discount *int   `json:"discount_percent"`
}

type details struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) fetch(ctx context.Context, ids []int64, country, filter string) (map[string]details, string, error) {
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = strconv.FormatInt(id, 10)
	}

	q := url.Values{
		"appids":  {strings.Join(values, ",")},
		"cc":      {country},
		"l":       {"english"},
		"filters": {filter},
	}
	body, err := c.request(ctx, c.DetailsURL, q, "")
	if err != nil {
		return nil, "", err
	}

	var result map[string]details
	if err := json.Unmarshal(body, &result); err != nil || result == nil {
		return nil, "", errors.New("steam returned invalid app details")
	}

	return result, fmt.Sprintf("%x", sha256.Sum256(body)), nil
}

func (c *Client) Prices(ctx context.Context, ids []int64, country string) (map[int64]domain.Price, error) {
	result := make(map[int64]domain.Price, len(ids))
	if len(ids) == 0 {
		return result, nil
	}

	country, err := NormalizeCountry(country)
	if err != nil {
		return nil, err
	}

	entries, hash, err := c.fetch(ctx, ids, country, "price_overview")
	if err != nil {
		return nil, err
	}

	for _, id := range ids {
		entry := entries[strconv.FormatInt(id, 10)]
		p := domain.Price{
			AppID:        id,
			Country:      country,
			Availability: "unknown",
			SaleState:    "unknown",
			ObservedAt:   time.Now().UTC(),
			SourceStatus: "missing_or_unpriced",
			SourceHash:   hash,
		}
		var data struct {
			Overview *overview `json:"price_overview"`
		}
		if entry.Success && json.Unmarshal(entry.Data, &data) == nil && data.Overview != nil {
			p = normalize(p, *data.Overview)
		}

		result[id] = p
	}

	return result, nil
}

func normalize(p domain.Price, o overview) domain.Price {
	p.Currency = strings.ToUpper(o.Currency)
	p.Regular = o.Initial
	p.Final = o.Final
	p.Discount = o.Discount
	p.SourceStatus = "incoherent_price"
	if p.Currency == "" || o.Initial == nil || o.Final == nil || o.Discount == nil || *o.Initial < 0 || *o.Final < 0 || *o.Discount < 0 {
		return p
	}

	sale := *o.Discount > 0 && *o.Final < *o.Initial
	off := *o.Discount == 0 && *o.Final == *o.Initial
	if !sale && !off {
		return p
	}

	p.Availability = "priced"
	p.SourceStatus = "ok"
	p.SaleState = "not_on_sale"
	if sale {
		p.SaleState = "on_sale"
	}

	return p
}

func (c *Client) Details(ctx context.Context, id int64, country string) (domain.Price, error) {
	country, err := NormalizeCountry(country)
	if err != nil {
		return domain.Price{}, err
	}

	key := fmt.Sprintf("%d:%s", id, country)

	c.mu.Lock()
	cached, ok := c.cache[key]
	c.mu.Unlock()

	if ok && time.Now().Before(cached.expires) {
		c.Diagnostics.Observe("cache", "price-hit", 0, nil)
		return cached.price, nil
	}
	c.Diagnostics.Observe("cache", "price-miss", 0, nil)

	data, err := c.basic(ctx, id, country)
	if err != nil {
		return domain.Price{}, err
	}

	prices, err := c.Prices(ctx, []int64{id}, country)
	if err != nil {
		return domain.Price{}, err
	}

	p := prices[id]
	p.Name = data.Name
	p.Type = strings.ToLower(data.Type)
	p.CapsuleURL = data.CapsuleURL

	paid := p.Availability == "priced" && p.Regular != nil && *p.Regular > 0
	if data.Free && !paid {
		p.Availability = "free"
		p.SaleState = "unknown"
	}

	c.mu.Lock()
	for k, v := range c.cache {
		if time.Now().After(v.expires) || int64(len(c.cache)) >= c.cfg.PriceCacheMaximumEntries {
			delete(c.cache, k)
		}
	}
	c.cache[key] = cacheEntry{
		price:   p,
		expires: time.Now().Add(c.cfg.PriceCacheTTL),
	}
	c.mu.Unlock()

	return p, nil
}
