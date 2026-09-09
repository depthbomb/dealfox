package commands

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/rest"
	"github.com/depthbomb/tomogo/testkit"
)

func TestFreeGameScopesRequireManageServer(t *testing.T) {
	for _, tc := range []struct {
		name        string
		guild       api.ID
		permissions api.Permissions
		allowed     bool
	}{
		{
			name:        "DM cannot configure channel",
			permissions: api.PermissionAdministrator,
		},
		{
			name:  "ordinary guild member",
			guild: 456,
		},
		{
			name:        "Manage Channels is insufficient",
			guild:       456,
			permissions: api.PermissionManageChannels,
		},
		{
			name:        "Manage Server",
			guild:       456,
			permissions: api.PermissionManageGuild,
			allowed:     true,
		},
		{
			name:        "administrator",
			guild:       456,
			permissions: api.PermissionAdministrator,
			allowed:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := &api.Interaction{
				GuildID: tc.guild,
				Member: &api.GuildMember{
					Permissions: tc.permissions,
				},
			}
			scope, err := freeScope(i, "123", true)
			if (err == nil) != tc.allowed || tc.allowed && scope != "guild:456" {
				t.Fatalf("server scope: %s %v", scope, err)
			}
			scope, err = freeScope(i, "123", false)
			if err != nil || scope != "dm:123" {
				t.Fatalf("DM scope unavailable: %s %v", scope, err)
			}
		})
	}
}

func TestFreeGameDMCommandWorkflow(t *testing.T) {
	db, _ := testutil.Database(t)
	_, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
		Store:  db,
	})
	dispatch(t, app, "freegames", testkit.Subcommand("subscribe", testkit.StringOption("sources", "steam,gog")))
	subs, err := db.FreeSubscriptions(t.Context(), "dm:123")
	if err != nil || len(subs) != 2 {
		t.Fatalf("source selection failed: %+v %v", subs, err)
	}
	dispatch(t, app, "freegames", testkit.Subcommand("subscribe", testkit.StringOption("sources", "amazon")))
	dispatch(t, app, "freegames", testkit.Subcommand("unsubscribe", testkit.StringOption("sources", "gog")))
	subs, err = db.FreeSubscriptions(t.Context(), "dm:123")
	if err != nil || len(subs) != 1 || subs[0].Source != "steam" {
		t.Fatalf("source removal failed: %+v %v", subs, err)
	}
	dispatch(t, app, "freegames", testkit.Subcommand("subscribe", testkit.StringOption("sources", "all")))
	subs, err = db.FreeSubscriptions(t.Context(), "dm:123")
	if err != nil || len(subs) != 4 {
		t.Fatalf("all source selection failed: %+v %v", subs, err)
	}
}

func TestFreeChannelValidation(t *testing.T) {
	for _, test := range []struct {
		name        string
		channelType api.ChannelType
		guild       api.ID
		deny        api.Permissions
		flags       api.ChannelFlags
		status      int
		missingBot  bool
		want        string
	}{
		{
			name:  "text",
			guild: 456,
		},
		{
			name:        "announcement",
			guild:       456,
			channelType: api.ChannelGuildAnnouncement,
		},
		{
			name:  "wrong guild",
			guild: 999,
			want:  "Choose a text or announcement channel in this server.",
		},
		{
			name:        "voice",
			guild:       456,
			channelType: api.ChannelGuildVoice,
			want:        "Choose a text or announcement channel in this server.",
		},
		{
			name:  "missing view",
			guild: 456,
			deny:  api.PermissionViewChannel,
			want:  "I need View Channel, Send Messages, and Embed Links in that channel.",
		},
		{
			name:  "missing send",
			guild: 456,
			deny:  api.PermissionSendMessages,
			want:  "I need View Channel, Send Messages, and Embed Links in that channel.",
		},
		{
			name:  "missing embeds",
			guild: 456,
			deny:  api.PermissionEmbedLinks,
			want:  "I need View Channel, Send Messages, and Embed Links in that channel.",
		},
		{
			name:  "obfuscated",
			guild: 456,
			flags: api.ChannelFlagObfuscated,
			want:  "I need View Channel, Send Messages, and Embed Links in that channel.",
		},
		{
			name:   "inaccessible",
			guild:  456,
			status: 403,
			want:   "I couldn't access that channel. Make sure I'm installed in this server and can view it.",
		},
		{
			name:       "missing bot identity",
			guild:      456,
			missingBot: true,
			want:       "Channel validation is unavailable. Please try again later.",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			channelFetches := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/channels/789":
					channelFetches++
					if test.status != 0 {
						w.WriteHeader(test.status)
						fmt.Fprint(w, `{"code":50013,"message":"Missing Permissions"}`)

						return
					}
					fmt.Fprintf(w, `{"id":"789","guild_id":"%s","type":%d,"flags":%d,"permission_overwrites":[{"id":"456","type":0,"allow":"0","deny":"%d"}]}`, test.guild, test.channelType, test.flags, test.deny)
				case "/guilds/456":
					fmt.Fprint(w, `{"id":"456","owner_id":"123","roles":[{"id":"456","position":0,"permissions":"19456"}]}`)
				case "/users/@me":
					id := "111"
					if test.missingBot {
						id = "0"
					}
					fmt.Fprintf(w, `{"id":"%s"}`, id)
				case "/guilds/456/members/111":
					fmt.Fprint(w, `{"user":{"id":"111"},"roles":[]}`)
				default:
					t.Error("unexpected lookup, possibly invoking user or another guild", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			b := &Handler{
				REST: rest.New(rest.Config{
					Token:   "test",
					BaseURL: server.URL,
				}),
			}
			i := &api.Interaction{
				GuildID: 456,
				User: &api.User{
					ID: 123,
				},
			}
			err := b.verifyFreeChannel(t.Context(), i, 789)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				message, public := commandErrorMessage(err)
				if !public || message != test.want {
					t.Fatalf("permission error = %v, public message = %q", err, message)
				}
			}
			wantFetches := 1
			if test.missingBot {
				wantFetches = 0
			}
			if channelFetches != wantFetches {
				t.Fatalf("channel fetched %d times, want %d", channelFetches, wantFetches)
			}
		})
	}
}

func TestFreeGameChannelOptionDoesNotRequireResolvedData(t *testing.T) {
	db, _ := testutil.Database(t)
	h, app := newTestHandler(t, &tracker.Service{
		Config: testutil.Config(t),
		Store:  db,
	})
	channelFetches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/channels/789":
			channelFetches++
			fmt.Fprint(w, `{"id":"789","guild_id":"456","type":0,"permission_overwrites":[]}`)
		case "/guilds/456":
			fmt.Fprint(w, `{"id":"456","owner_id":"123","roles":[{"id":"456","permissions":"19456"}]}`)
		case "/users/@me":
			fmt.Fprint(w, `{"id":"111"}`)
		case "/guilds/456/members/111":
			fmt.Fprint(w, `{"user":{"id":"111"},"roles":[]}`)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	h.REST = rest.New(rest.Config{
		Token:   "test",
		BaseURL: server.URL,
	})
	i := testkit.CommandInteraction("freegames", testkit.Subcommand("subscribe", testkit.StringOption("sources", "steam"), testkit.SnowflakeOption("channel", api.OptionChannel, 789)))
	i.GuildID = 456
	i.Member = &api.GuildMember{
		User: &api.User{
			ID: 123,
		},
		Permissions: api.PermissionManageGuild,
	}
	harness := testkit.NewInteractionHarness(i)
	if err := app.DispatchInteraction(t.Context(), i, harness.Responder); err != nil {
		t.Fatal(err)
	}
	subscriptions, err := db.FreeSubscriptions(t.Context(), "guild:456")
	if err != nil || len(subscriptions) != 1 || subscriptions[0].DestinationID != "789" || channelFetches != 1 {
		t.Fatalf("typed channel option failed: subscriptions=%v fetches=%d error=%v", subscriptions, channelFetches, err)
	}
}
