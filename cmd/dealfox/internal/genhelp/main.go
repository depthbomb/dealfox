package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/depthbomb/argon/helpgen"
	"github.com/depthbomb/dealfox/internal/app"
)

func main() {
	application := app.New(io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := helpgen.Run(application, helpgen.Options{}, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
