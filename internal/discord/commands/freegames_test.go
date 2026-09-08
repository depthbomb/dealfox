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
	denied := false
	wrongGuild := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/channels/789":
			guild := "456"
			if wrongGuild {
				guild = "999"
			}
			deny := 0
			if denied {
				deny = int(api.PermissionEmbedLinks)
			}
			fmt.Fprintf(w, `{"id":"789","guild_id":"%s","type":0,"permission_overwrites":[{"id":"456","type":0,"allow":"0","deny":"%d"}]}`, guild, deny)
		case "/guilds/456":
			fmt.Fprint(w, `{"id":"456","owner_id":"123","roles":[{"id":"456","position":0,"permissions":"19456"}]}`)
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
	client := rest.New(rest.Config{
		Token:   "test",
		BaseURL: server.URL,
	})
	b := &Handler{
		REST: client,
	}
	i := &api.Interaction{
		GuildID: 456,
	}
	if err := b.verifyFreeChannel(t.Context(), i, 789); err != nil {
		t.Fatal(err)
	}
	denied = true
	if err := b.verifyFreeChannel(t.Context(), i, 789); err == nil {
		t.Fatal("missing Embed Links permission accepted")
	}
	denied = false
	wrongGuild = true
	if err := b.verifyFreeChannel(t.Context(), i, 789); err == nil {
		t.Fatal("cross-server channel accepted")
	}
}
