package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/depthbomb/dealfox/internal/domain"
)

type basicDetails struct {
	ID           int64  `json:"steam_appid"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Free         bool   `json:"is_free"`
	HeaderImage  string `json:"header_image"`
	CapsuleImage string `json:"capsule_image"`
	CapsuleURL   string `json:"-"`
}

func (c *Client) basic(ctx context.Context, id int64, country string) (basicDetails, error) {
	entries, _, err := c.fetch(ctx, []int64{id}, country, "basic")
	if err != nil {
		return basicDetails{}, err
	}

	entry := entries[strconv.FormatInt(id, 10)]
	var data basicDetails
	if !entry.Success || json.Unmarshal(entry.Data, &data) != nil || data.ID != id || data.Name == "" {
		return basicDetails{}, &domain.PublicError{
			Code:    "NOT_FOUND",
			Message: "That Steam item was not found.",
		}
	}

	for _, image := range []string{data.HeaderImage, data.CapsuleImage} {
		u, err := url.Parse(image)
		if err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil {
			data.CapsuleURL = image
			break
		}
	}

	data.CapsuleURL = c.rememberArtwork(fmt.Sprintf("%d:%s", id, country), data.CapsuleURL)

	return data, nil
}
