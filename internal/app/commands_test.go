package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/depthbomb/dealfox/internal/config"
	"github.com/depthbomb/dealfox/internal/discord/commands"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
)

type publicationTransport func(*http.Request) (*http.Response, error)

func (f publicationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCommandPublicationScopes(t *testing.T) {
	cfg, err := config.LoadFrom(func(name string) (string, bool) {
		switch name {
		case "BOT_TOKEN":
			return "test-token", true
		case "DISCORD_APPLICATION_ID":
			return "123", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	original := http.DefaultTransport
	t.Cleanup(func() {
		http.DefaultTransport = original
	})
	for _, guild := range []string{"", "456"} {
		for _, confirm := range []bool{false, true} {
			name := "global"
			if guild != "" {
				name = "guild"
			}

			if confirm {
				name += "/replace"
			} else {
				name += "/compare"
			}
			t.Run(name, func(t *testing.T) {
				handler := commands.Handler{
					Tracker: &tracker.Service{
						Config: &cfg,
					},
				}
				expected, err := handler.Definitions()
				if err != nil {
					t.Fatal(err)
				}
				path := "/api/v10/applications/123"
				if guild != "" {
					path += "/guilds/456"
					for index := range expected {
						expected[index].Contexts = nil
						expected[index].IntegrationTypes = nil
					}
				}
				path += "/commands"
				calls := 0
				http.DefaultTransport = publicationTransport(func(request *http.Request) (*http.Response, error) {
					calls++
					method := http.MethodGet
					if confirm {
						method = http.MethodPut
						var published []api.ApplicationCommand
						if err := json.NewDecoder(request.Body).Decode(&published); err != nil {
							t.Fatal(err)
						}

						if !reflect.DeepEqual(published, expected) {
							t.Fatal("published definitions changed or used the wrong scope")
						}
					}

					if request.Method != method || request.URL.Path != path {
						t.Fatalf("unexpected publication request: %s %s", request.Method, request.URL.Path)
					}

					remote := append([]api.ApplicationCommand{}, expected...)
					for index := range remote {
						remote[index].ID = api.ID(1000 + index)
						remote[index].ApplicationID = 123
						remote[index].Version = 789
						if guild != "" {
							remote[index].GuildID = 456
						}
					}
					body, err := json.Marshal(remote)
					if err != nil {
						t.Fatal(err)
					}

					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{},
						Body:       io.NopCloser(bytes.NewReader(body)),
					}, nil
				})
				var output bytes.Buffer
				if err := publish(t.Context(), &cfg, guild, confirm, &output); err != nil {
					t.Fatal(err)
				}

				if calls != 1 {
					t.Fatalf("expected one publication request, got %d", calls)
				}

				if !confirm {
					for _, definition := range expected {
						if !strings.Contains(output.String(), "unchanged "+definition.Name+"\n") {
							t.Fatalf("Discord identity fields caused false drift: %s", output.String())
						}
					}
				}
			})
		}
	}
}
