package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestCLIHelpWithoutConfiguration(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("POLL_INTERVAL=invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLL_INTERVAL", "invalid")
	for _, args := range [][]string{
		{},
		{"--help"},
		{"-h"},
		{"help", "database"},
		{"database"},
		{"catalog"},
		{"deliveries"},
		{"freegames"},
		{"serve", "--help"},
		{"database", "create", "--help"},
		{"database", "migrate", "--help"},
		{"database", "status", "--help"},
		{"catalog", "sync", "--help"},
		{"deliveries", "list-dead", "--help"},
		{"deliveries", "retry", "--help"},
		{"deliveries", "cancel", "--help"},
		{"commands", "--help"},
		{"freegames", "scan", "--help"},
		{"freegames", "status", "--help"},
		{"freegames", "delivery", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			args = append([]string{"--config-dir", directory}, args...)
			if err := run(t.Context(), args, &output, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(output.String(), "Usage:") || !strings.Contains(output.String(), "dealfox") {
				t.Fatalf("missing generated help: %s", output.String())
			}
		})
	}
}

func TestCLIErrorsReturnToMain(t *testing.T) {
	var output bytes.Buffer
	err := run(t.Context(), []string{"serve", "--unknown"}, &output, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || output.Len() != 0 {
		t.Fatalf("error was swallowed or printed to stdout: %v, %q", err, output.String())
	}

	failure := errors.New("output failed")
	err = run(t.Context(), []string{"--help"}, failingWriter{
		err: failure,
	}, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("help output error was lost: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = run(ctx, []string{"serve"}, io.Discard, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was lost: %v", err)
	}
}
