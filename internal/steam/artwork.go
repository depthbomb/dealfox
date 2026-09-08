package steam

import (
	"context"
	"fmt"
	"time"
)

type artworkEntry struct {
	url       string
	refreshAt time.Time
	lastUsed  time.Time
}

func (c *Client) cachedArtwork(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.artwork[key]
	if !ok {
		return "", false
	}

	entry.lastUsed = time.Now()
	c.artwork[key] = entry

	return entry.url, entry.lastUsed.Before(entry.refreshAt)
}

func (c *Client) rememberArtwork(key, image string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.artwork[key]
	if !exists && int64(len(c.artwork)) >= c.cfg.ArtworkCacheMaximumEntries {
		var oldest string
		for candidate, value := range c.artwork {
			if oldest == "" || value.lastUsed.Before(c.artwork[oldest].lastUsed) {
				oldest = candidate
			}
		}
		delete(c.artwork, oldest)
	}

	now := time.Now()
	entry.lastUsed = now
	entry.refreshAt = now.Add(c.cfg.ArtworkCacheRetryInterval)

	if image != "" {
		entry.url = image
		entry.refreshAt = now.Add(c.cfg.ArtworkCacheTTL)
	}

	c.artwork[key] = entry

	return entry.url
}

// Artwork returns a cached capsule URL, retaining the last known URL if refresh fails.
// Concurrent refreshes share a bounded request; price data is never fetched here.
func (c *Client) Artwork(ctx context.Context, id int64, country string) (string, error) {
	country, err := NormalizeCountry(country)
	if err != nil {
		return "", err
	}

	key := fmt.Sprintf("%d:%s", id, country)
	cached, fresh := c.cachedArtwork(key)
	if fresh {
		c.Diagnostics.Observe("cache", "artwork-hit", 0, nil)
		return cached, nil
	}
	c.Diagnostics.Observe("cache", "artwork-miss", 0, nil)

	result := c.artworkRequests.DoChan(key, func() (any, error) {
		cached, fresh := c.cachedArtwork(key)
		if fresh {
			return cached, nil
		}

		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.cfg.SteamTimeout)
		defer cancel()
		data, err := c.basic(refreshCtx, id, country)
		if err != nil {
			cached = c.rememberArtwork(key, "")
			if cached != "" {
				return cached, nil
			}

			return "", err
		}

		return data.CapsuleURL, nil
	})
	select {
	case <-ctx.Done():
		if cached != "" {
			return cached, nil
		}

		return "", ctx.Err()
	case result := <-result:
		return result.Val.(string), result.Err
	}
}
