package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/depthbomb/dealfox/internal/diagnostics"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
	"github.com/depthbomb/tomogo/api"
	"github.com/depthbomb/tomogo/interactions"
	"github.com/depthbomb/tomogo/testkit"
)

func TestCommandDiagnosticsCoverSuccessRejectionAndSupportReference(t *testing.T) {
	dir := t.TempDir()
	recorder, err := diagnostics.Open(diagnostics.Options{
		Directory: dir,
		MaxBytes:  1 << 20,
		MaxFiles:  2,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testutil.Config(t)
	cfg.CommandRateLimit = 2
	handler, app := newTestHandler(t, &tracker.Service{
		Config: cfg,
		Steam: steamStub{
			err: errors.New("private-upstream-error"),
		},
	})
	handler.Diagnostics = recorder
	dispatch(t, app, "about")
	failed := dispatch(t, app, "price", testkit.StringOption("game", "10"))
	requireRateDenial(t, invokeLimitedCommand(t, app, 123, 0, "track", testkit.Subcommand("add", testkit.StringOption("game", "private-game"))))
	response, err := json.Marshal(failed.OriginalEdits())
	if err != nil || !bytes.Contains(response, []byte("Reference:")) {
		t.Fatal("command failure lost its support reference")
	}
	if err := recorder.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "beta.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-")) {
		t.Fatal("command diagnostics exposed request or error contents")
	}
	for _, want := range []string{`"name":"about"`, `"name":"track.add"`, `"category":"limit"`, `"outcome":"rejected"`, `"event":"reference"`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("missing diagnostic %s", want)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event struct {
			Reference string `json:"reference"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}

		if event.Reference != "" && !bytes.Contains(response, []byte(event.Reference)) {
			t.Fatal("diagnostic reference did not match the user's error response")
		}
	}
}

func TestObserverCountsPanicAndContinuationSeparately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		recorder, err := diagnostics.Open(diagnostics.Options{
			Directory: dir,
			MaxBytes:  1 << 20,
			MaxFiles:  2,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		h, app := newTestHandler(t, &tracker.Service{
			Config: testutil.Config(t),
		})
		h.Diagnostics = recorder
		configureAccountHTTP(t, h, &accountHTTP{})
		invokeLimitedCommand(t, app, 123, 0, "account", testkit.Subcommand("delete"))
		synctest.Wait()
		if pendingAccounts(h) != 1 {
			t.Fatal("continuation did not remain active")
		}
		if err := app.Commands().Replace(api.ApplicationCommand{
			Name:        "about",
			Description: "Test panic observation",
		}, func(context.Context, *api.Interaction, *interactions.Responder) error {
			panic("private-panic")
		}); err != nil {
			t.Fatal(err)
		}
		panicResponse := invokeLimitedCommand(t, app, 999, 0, "about")
		if !strings.Contains(*panicResponse.InitialResponses()[0].Response.Data.Embeds[0].Description, "Reference:") {
			t.Fatal("panic lost its support reference")
		}
		time.Sleep(deletionConfirmationTimeout)
		synctest.Wait()
		if err := recorder.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "beta.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("private-")) {
			t.Fatal("observer recorded the panic contents")
		}
		counts := make(map[string]uint64)
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var event struct {
				Summary []struct {
					Category string
					Name     string
					Outcome  string
					Count    uint64
				}
			}
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatal(err)
			}
			for _, item := range event.Summary {
				if item.Category == "command" || item.Category == "continuation" {
					counts[item.Category+"/"+item.Name+"/"+item.Outcome] += item.Count
				}
			}
		}
		for _, key := range []string{"command/account.delete/ok", "command/about/panic", "continuation/account.delete/timeout"} {
			if counts[key] != 1 {
				t.Fatalf("expected one %s measurement, got %v", key, counts)
			}
		}
		if len(counts) != 3 {
			t.Fatalf("duplicate or unexpected command observation: %v", counts)
		}
	})
}
