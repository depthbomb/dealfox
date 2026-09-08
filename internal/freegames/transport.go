package freegames

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/depthbomb/dealfox/internal/diagnostics"
)

type cachedResponse struct {
	body  []byte
	until time.Time
	url   string
}

type hostState struct {
	failures int
	after    time.Time
}

type Client struct {
	http  *http.Client
	mu    sync.Mutex
	cache map[string]cachedResponse
	hosts map[string]hostState
	clock func() time.Time
}

func allowedURL(source, raw string, image bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}

	domains := map[string][]string{
		"steam":   {"steampowered.com", "steamcommunity.com", "steamstore-a.akamaihd.net"},
		"epic":    {"epicgames.com"},
		"gog":     {"gog.com"},
		"ubisoft": {"ubisoft.com", "ubi.com"},
	}
	if image {
		domains["steam"] = append(domains["steam"], "steamstatic.com", "steamusercontent.com")
		domains["epic"] = append(domains["epic"], "unrealengine.com")
		domains["gog"] = append(domains["gog"], "gog-statics.com")
	}

	for _, domain := range domains[source] {
		if u.Hostname() == domain || strings.HasSuffix(u.Hostname(), "."+domain) {
			return true
		}
	}

	return false
}

func (c *Client) get(ctx context.Context, source, raw string, ttl time.Duration) ([]byte, Evidence, error) {
	evidence := Evidence{
		URL: raw,
	}
	if !allowedURL(source, raw, false) {
		return nil, evidence, errors.New("source URL is outside its first-party boundary")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	if entry, ok := c.cache[raw]; ok && now.Before(entry.until) {
		evidence.Hash = fmt.Sprintf("%x", sha256.Sum256(entry.body))
		evidence.URL = entry.url

		return entry.body, evidence, nil
	}

	u, _ := url.Parse(raw)
	state := c.hosts[u.Hostname()]
	if now.Before(state.after) {
		return nil, evidence, errors.New("source is in backoff")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, evidence, err
	}
	req.Header.Set("User-Agent", "DealFox/1.0 (+https://github.com/depthbomb/dealfox; free-game monitoring)")
	req.Header.Set("Accept", "application/json, text/html;q=0.9")
	if u.Hostname() == "public-ubiservices.ubi.com" {
		req.Header.Set("ubi-appid", "5c5d3b21-e1fc-4460-9213-87b4cd440d44")
		req.Header.Set("ubi-localeCode", "en-US")
	}

	client := *c.http
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowedURL(source, req.URL.String(), false) {
			return errors.New("unapproved source redirect")
		}

		return nil
	}
	response, err := client.Do(req)
	if err != nil {
		state.failures++
		state.after = now.Add(time.Duration(1<<min(state.failures, 5)) * 15 * time.Minute)
		c.hosts[u.Hostname()] = state

		return nil, evidence, fmt.Errorf("%s request failed", u.Hostname())
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		state.failures++
		delay := time.Duration(1<<min(state.failures, 5)) * 15 * time.Minute
		if response.StatusCode == 403 && state.failures >= 3 {
			delay = 24 * time.Hour
		}

		retry := response.Header.Get("Retry-After")
		if seconds, err := strconv.ParseInt(retry, 10, 32); err == nil && seconds > 0 {
			delay = max(delay, time.Duration(seconds)*time.Second)
		} else if at, err := http.ParseTime(retry); err == nil {
			delay = max(delay, at.Sub(now))
		}
		state.after = now.Add(delay)
		c.hosts[u.Hostname()] = state

		return nil, evidence, fmt.Errorf("%s returned HTTP %d", u.Hostname(), response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil || len(body) > 8*1024*1024 {
		return nil, evidence, errors.New("source response unreadable or exceeds 8 MiB")
	}

	c.hosts[u.Hostname()] = hostState{}
	evidence.URL = response.Request.URL.String()
	evidence.Hash = fmt.Sprintf("%x", sha256.Sum256(body))

	if ttl > 0 {
		if len(c.cache) >= 1000 {
			for key := range c.cache {
				delete(c.cache, key)
				break
			}
		}

		c.cache[raw] = cachedResponse{
			body:  body,
			until: now.Add(ttl),
			url:   evidence.URL,
		}
	}

	return body, evidence, nil
}

func NewClient(recorders ...*diagnostics.Recorder) *Client {
	var transport http.RoundTripper
	if len(recorders) > 0 && recorders[0] != nil {
		transport = diagnostics.Transport{
			Recorder: recorders[0],
			Service:  "freegames",
		}
	}

	return &Client{
		http: &http.Client{
			Timeout:   20 * time.Second,
			Transport: transport,
		},
		cache: make(map[string]cachedResponse),
		hosts: make(map[string]hostState),
		clock: time.Now,
	}
}

func (c *Client) Scan(ctx context.Context, source string) Result {
	switch source {
	case "steam":
		return c.steam(ctx)
	case "epic":
		return c.epic(ctx)
	case "gog":
		return c.gog(ctx)
	case "ubisoft":
		return c.ubisoft(ctx)
	}

	return Result{
		Problems: []string{"unsupported source"},
	}
}
