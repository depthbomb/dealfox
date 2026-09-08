package steam

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func (c *Client) request(ctx context.Context, endpoint string, query url.Values, key string) ([]byte, error) {
	select {
	case c.admission <- struct{}{}:
		defer func() {
			<-c.admission
		}()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	c.mu.Lock()
	open := time.Now().Before(c.openUntil)
	c.mu.Unlock()
	if open {
		c.Diagnostics.Observe("cache", "steam-circuit-open", 0, nil)
		return nil, errors.New("steam circuit is temporarily open")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "dealfox/1.0 (Steam price tracker)")
	if key != "" {
		req.Header.Set("X-Webapi-Key", key)
	}

	resp, err := c.HTTP.Do(req)
	var body []byte
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err = fmt.Errorf("steam HTTP %d", resp.StatusCode)
		} else {
			body, err = io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
			if len(body) > 32<<20 {
				err = errors.New("steam response exceeds size limit")
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.failures++
		if c.failures >= c.cfg.SteamCircuitFailureThreshold {
			c.openUntil = time.Now().Add(c.cfg.SteamCircuitOpenDuration)
			c.failures = 0
		}

		return nil, errors.New("steam request failed")
	}

	c.failures = 0
	c.openUntil = time.Time{}

	return body, nil
}
