//go:generate go run ./internal/genhelp

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/depthbomb/dealfox/internal/app"
)

func run(ctx context.Context, args []string, output io.Writer, logger *slog.Logger) error {
	application := app.New(output, logger)
	application.Config.HelpText = generatedHelp
	result := application.Evaluate(ctx, args)
	if result.Err != nil {
		return result.Err
	}

	if result.Text != "" {
		_, err := io.WriteString(output, result.Text)

		return err
	}

	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(ctx, os.Args[1:], os.Stdout, logger); err != nil {
		fmt.Fprintln(os.Stderr, "dealfox:", err)
		os.Exit(1)
	}
}
