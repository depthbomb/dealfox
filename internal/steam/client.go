package steam

import (
	"net/http"
	"sync"
	"time"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/diagnostics"
	"github.com/depthbomb/dealfox/internal/domain"
	"golang.org/x/sync/singleflight"
)

type cacheEntry struct {
	price   domain.Price
	expires time.Time
}

type Client struct {
	HTTP            *http.Client
	DetailsURL      string
	CatalogURL      string
	Diagnostics     *diagnostics.Recorder
	cfg             *config.Config
	admission       chan struct{}
	mu              sync.Mutex
	cache           map[string]cacheEntry
	artwork         map[string]artworkEntry
	artworkRequests singleflight.Group
	failures        int64
	openUntil       time.Time
}

func New(cfg *config.Config) *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: cfg.SteamTimeout,
		},
		DetailsURL: "https://store.steampowered.com/api/appdetails",
		CatalogURL: "https://api.steampowered.com/IStoreService/GetAppList/v1/",
		cfg:        cfg,
		admission:  make(chan struct{}, cfg.SteamMaximumConcurrency),
		cache:      make(map[string]cacheEntry),
		artwork:    make(map[string]artworkEntry),
	}
}
