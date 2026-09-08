package freegames

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/depthbomb/dealfox/internal/domain"
)

type Evidence struct {
	URL    string
	Hash   string
	Basis  string
	Record json.RawMessage
}

type Offer struct {
	Source        string
	ProductID     string
	Campaign      string
	Country       string
	Platform      string
	Title         string
	Description   string
	ImageURL      string
	URL           string
	ClaimURL      string
	StartsAt      time.Time
	EndsAt        time.Time
	ObservedAt    time.Time
	Eligible      bool
	Reason        string
	Evidence      []Evidence
	ParserVersion int
}

type Result struct {
	Offers   []Offer
	Problems []string
}

func recordEvidence(e Evidence, basis string, value any) Evidence {
	e.Basis = basis
	e.Record, _ = json.Marshal(value)

	return e
}

func (o Offer) Key() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join([]string{o.Source, o.Country, o.Platform, o.ProductID, o.Campaign}, ":"))))
}

func (o Offer) Publishable(now time.Time) bool {
	return o.Eligible && o.ProductID != "" && o.Campaign != "" && o.Country == "US" && o.Platform != "" && o.Title != "" && o.Description != "" && !strings.ContainsRune(o.Title+o.Description, '\uFFFD') && allowedURL(o.Source, o.URL, false) && allowedURL(o.Source, o.ImageURL, true) && !now.Before(o.StartsAt) && (o.EndsAt.IsZero() || now.Before(o.EndsAt)) && len(o.Evidence) > 0
}

func Sources() []string {
	return []string{"steam", "epic", "gog", "ubisoft"}
}

func ParseSources(input string) ([]string, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "all" {
		return Sources(), nil
	}

	var selected []string
	for _, value := range strings.Split(input, ",") {
		source := strings.TrimSpace(value)
		if !slices.Contains(Sources(), source) {
			return nil, domain.Invalid("Choose steam, epic, gog, ubisoft, a comma-separated selection, or all.")
		}

		if !slices.Contains(selected, source) {
			selected = append(selected, source)
		}
	}
	slices.Sort(selected)

	return selected, nil
}
