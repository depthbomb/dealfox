package steam

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/depthbomb/dealfox/internal/domain"
)

func NormalizeCountry(input string) (string, error) {
	country := strings.ToUpper(strings.TrimSpace(input))
	if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
		return "", domain.Invalid("Country must be a two-letter code such as US, GB, or DE.")
	}

	return country, nil
}

func ParseAppID(input string) (int64, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, domain.Invalid("Steam game or DLC is required.")
	}

	numeric := strings.Trim(input, "0123456789+-") == ""
	if strings.Contains(input, "://") {
		u, err := url.Parse(input)
		if err != nil || !strings.EqualFold(u.Hostname(), "store.steampowered.com") || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			return 0, domain.Invalid("Only Steam store app URLs are supported.")
		}

		parts := strings.Split(u.Path, "/")
		if len(parts) < 3 || parts[1] != "app" {
			return 0, domain.Invalid("Steam URL must have the form /app/<app-id>.")
		}

		input = parts[2]
		numeric = true
	}

	if !numeric {
		return 0, nil
	}

	id, err := strconv.ParseInt(input, 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.Invalid("Steam app ID must be a positive integer.")
	}

	return id, nil
}
