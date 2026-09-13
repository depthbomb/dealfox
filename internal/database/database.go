package database

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/depthbomb/dealfox/migrations"
	"github.com/depthbomb/nook"
	"github.com/depthbomb/nook/migrate"
)

func Path(path string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.Contains(path, "://") || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return "", errors.New("DATABASE_PATH must be a local SQLite file path")
	}

	return filepath.Abs(path)
}

func Options() nook.Options {
	timeout := 5 * time.Second

	return nook.Options{
		JournalMode:     nook.JournalWAL,
		WriteMode:       nook.WriteImmediate,
		BusyTimeout:     &timeout,
		ReadConnections: 2,
	}
}

func Create(ctx context.Context, path string) error {
	resolved, err := Path(path)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0700); err != nil {
		return err
	}
	// Set restrictive permissions at creation without changing an existing file.
	file, err := os.OpenFile(resolved, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}
	db, err := nook.OpenWithOptions(ctx, resolved, Options())
	if err != nil {
		return err
	}

	return db.Close()
}

// Migrate applies the checked SQLite history embedded in this binary.
func Migrate(ctx context.Context, path string, output io.Writer) error {
	history, err := migrate.Load(migrations.Files)
	if err != nil {
		return err
	}

	if err := Create(ctx, path); err != nil {
		return err
	}
	resolved, err := Path(path)
	if err != nil {
		return err
	}
	db, err := nook.OpenWithOptions(ctx, resolved, Options())
	if err != nil {
		return err
	}
	defer db.Close()
	if err := migrate.Apply(ctx, db.DB, history); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "Database migrations are current and verified.")

	return err
}
