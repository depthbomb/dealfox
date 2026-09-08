package embeds

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/depthbomb/tomogo/api"
	embedbuilder "github.com/tomogo-framework/embed-builder"
)

func TestBuildPreservesEmbedPayload(t *testing.T) {
	for _, builder := range []*embedbuilder.Builder{
		embedbuilder.New().SetDescription("Text only"),
		embedbuilder.New().SetTitle("Game").SetDescription("50% off").SetURL("https://example.com/game").SetColor(0).SetTimestamp(time.Date(2026, 9, 8, 12, 30, 0, 0, time.UTC)).SetFooter("US offer", "https://example.com/footer.png").SetImage("https://example.com/game.png").SetThumbnail("https://example.com/thumbnail.png").SetAuthor("DealFox", "https://example.com", "https://example.com/author.png").AddField("Price", "$10.00", true),
	} {
		encoded, err := builder.BuildJSON()
		if err != nil {
			t.Fatal(err)
		}
		var previous api.Embed
		if err := json.Unmarshal(encoded, &previous); err != nil {
			t.Fatal(err)
		}
		current, err := Build(builder)
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(current, previous) {
			t.Fatalf("embed payload changed: got %+v, want %+v", current, previous)
		}
	}
}

func TestBuildRejectsInvalidEmbeds(t *testing.T) {
	_, err := Build(nil)
	if !errors.Is(err, embedbuilder.ErrNilBuilder) {
		t.Fatalf("nil builder error = %v", err)
	}

	if _, err := Build(embedbuilder.New().SetTitle(strings.Repeat("x", 257))); err == nil {
		t.Fatal("oversized embed was accepted")
	}
}
