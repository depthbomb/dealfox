package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/depthbomb/dealfox/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := app.New(os.Stdout, logger).Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "dealfox:", err)
		os.Exit(1)
	}
}
