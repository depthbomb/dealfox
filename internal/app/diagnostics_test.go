package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/depthbomb/dealfox/internal/diagnostics"
	"github.com/depthbomb/tomogo/schedule"
)

func TestJobDiagnosticsPreservePanicAndExpiredContextBehavior(t *testing.T) {
	dir := t.TempDir()
	recorder, err := diagnostics.Open(diagnostics.Options{
		Directory: dir,
		MaxBytes:  1 << 20,
		MaxFiles:  2,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	job := observeJob(recorder, periodicJob("poll", time.Second, time.Minute, func(context.Context) error {
		return nil
	}))
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if err := job.Handler(ctx, schedule.Run{
		Name: "poll",
	}); err != nil {
		t.Fatal("diagnostics changed the handler result")
	}
	job = observeJob(recorder, periodicJob("catalog", time.Hour, time.Hour, func(context.Context) error {
		panic("private-panic")
	}))
	func() {
		defer func() {
			if value := recover(); value != "private-panic" {
				t.Fatal("diagnostics changed panic propagation")
			}
		}()
		_ = job.Handler(t.Context(), schedule.Run{
			Name: "catalog",
		})
	}()
	if err := recorder.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "beta.jsonl"))
	if err != nil || bytes.Contains(data, []byte("private-panic")) || !bytes.Contains(data, []byte(`"outcome":"panic"`)) || !bytes.Contains(data, []byte(`"outcome":"timeout"`)) {
		t.Fatalf("job failure recording failed: %v", err)
	}
}
