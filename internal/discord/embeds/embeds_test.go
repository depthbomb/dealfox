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

func TestLimitPreservesUnicodeBudgetAndEllipsis(t *testing.T) {
	for _, test := range []struct {
		value string
		limit int
		want  string
	}{
		{
			value: "🦊Sale",
			limit: 5,
			want:  "🦊Sale",
		},
		{
			value: "🦊Sale",
			limit: 3,
			want:  "🦊S…",
		},
		{
			value: "🦊Sale",
			limit: 1,
			want:  "…",
		},
		{
			value: "Sale",
			limit: 0,
			want:  "",
		},
	} {
		if got := Limit(test.value, test.limit); got != test.want {
			t.Fatalf("Limit(%q, %d) = %q, want %q", test.value, test.limit, got, test.want)
		}
	}
}

func TestEscapeMarkdownPreservesExistingPunctuationPolicy(t *testing.T) {
	got := EscapeMarkdown("Game! 50% off. [Name](https://example.com/a-b) *sale*")
	want := "Game! 50% off. \\[Name\\](https://example.com/a-b) \\*sale\\*"
	if got != want {
		t.Fatalf("escape policy changed: %q", got)
	}
}
