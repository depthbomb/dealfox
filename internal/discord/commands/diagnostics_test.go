package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/depthbomb/dealfox/internal/diagnostics"
	"github.com/depthbomb/dealfox/internal/testutil"
	"github.com/depthbomb/dealfox/internal/tracker"
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
