package embeds

import (
	"fmt"
	"strings"

	"github.com/depthbomb/dealfox/internal/domain"
	embedadapter "github.com/depthbomb/tomogo/adapters/embedbuilder"
	"github.com/depthbomb/tomogo/api"
	embedbuilder "github.com/tomogo-framework/embed-builder"
)

var markdownEscaper = strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`", "~", "\\~", "|", "\\|", "[", "\\[", "]", "\\]")

func priceText(p domain.Price) string {
	if p.Availability != "priced" || p.Final == nil {
		return fmt.Sprintf("No trustworthy current Steam price in %s.", p.Country)
	}

	current := domain.Money{
		Minor:    *p.Final,
		Currency: p.Currency,
	}
	if p.SaleState == "on_sale" && p.Regular != nil && p.Discount != nil {
		regular := domain.Money{
			Minor:    *p.Regular,
			Currency: p.Currency,
		}

		return fmt.Sprintf("%d%% off in %s: %s → %s.", *p.Discount, p.Country, regular, current)
	}

	return fmt.Sprintf("Not currently on sale in %s. Current price: %s.", p.Country, current)
}

func Limit(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}

	return string(runes[:max-1]) + "…"
}

func Build(builder *embedbuilder.Builder) (api.Embed, error) {
	return embedadapter.Build(builder)
}

func Text(title, description string) (api.Embed, error) {
	return Build(embedbuilder.New().SetTitle(Limit(title, 256)).SetDescription(Limit(description, 4096)).SetColor(0xF28C28))
}

func Price(p domain.Price, description string) (api.Embed, error) {
	return Build(Game(p.AppID, p.CapsuleURL).SetTitle(Limit(p.Name, 256)).SetDescription(Limit(description+priceText(p), 4096)))
}

func Game(appID int64, capsuleURL string) *embedbuilder.Builder {
	builder := embedbuilder.New().SetURL(fmt.Sprintf("https://store.steampowered.com/app/%d", appID)).SetColor(0xF28C28)
	if capsuleURL != "" {
		builder.SetImage(capsuleURL)
	}

	return builder
}

func EscapeMarkdown(value string) string {
	return markdownEscaper.Replace(value)
}
