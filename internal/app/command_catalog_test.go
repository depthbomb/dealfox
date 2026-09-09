package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/tomogo/rest"
)

func TestCommandCatalogLoadsPublishedIDsWithoutPublication(t *testing.T) {
	for _, discover := range []bool{false, true} {
		cfg := testutil.Config(t)
		cfg.ApplicationID = new("123")
		if discover {
			cfg.ApplicationID = nil
		}
		calls := 0
		client := rest.New(rest.Config{
			Token: "test-token",
			HTTPClient: &http.Client{Transport: publicationTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodGet {
					t.Fatal("catalog loading attempted publication")
				}

				if _, ok := request.Context().Deadline(); !ok {
					t.Error("catalog loading has no deadline")
				}
				var body string
				switch request.URL.Path {
				case "/api/v10/applications/@me":
					if !discover {
						t.Error("ignored configured application ID")
					}
					body = `{"id":"123"}`
				case "/api/v10/applications/123/commands":
					body = `[{"id":"456","application_id":"123","name":"track","type":1,"options":[{"name":"list","type":1}]}]`
				default:
					t.Fatalf("unexpected catalog request: %s", request.URL.Path)
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			})},
		})
		catalog, err := commandCatalog(t.Context(), cfg, client)
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			mention, err := catalog.Mention("track", "list")
			if err != nil || mention != "</track list:456>" {
				t.Fatalf("published mention = %q, %v", mention, err)
			}
		}
		want := 1
		if discover {
			want = 2
		}

		if calls != want {
			t.Fatalf("catalog requests = %d, want %d", calls, want)
		}
	}
}

func TestCommandCatalogCancellation(t *testing.T) {
	cfg := testutil.Config(t)
	cfg.ApplicationID = new("123")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := rest.New(rest.Config{Token: "test-token"})
	catalog, err := commandCatalog(ctx, cfg, client)
	if catalog != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled catalog = %v, %v", catalog, err)
	}
}
