package database

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/depthbomb/dealfox/migrations"
	"github.com/jackc/pgx/v5"
)

func Create(ctx context.Context, dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return errors.New("DATABASE_URL must be a PostgreSQL URL")
	}

	name := strings.TrimPrefix(u.Path, "/")
	if name == "" || strings.ContainsAny(name, "/\x00") {
		return errors.New("DATABASE_URL must name a database")
	}

	u.Path = "/postgres"
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		return errors.New("could not connect to the postgres administrative database")
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil {
		return err
	}

	if exists {
		return nil
	}

	_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())

	return err
}

// Migrate runs Atlas against the migration files embedded in this binary.
func Migrate(ctx context.Context, dsn string, output io.Writer) error {
	if _, err := migrations.Hashes(); err != nil {
		return err
	}

	directory, err := os.MkdirTemp("", "dealfox-migrations-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}

	for _, entry := range entries {
		data, err := migrations.Files.ReadFile(entry.Name())
		if err != nil {
			return err
		}

		if err := os.WriteFile(filepath.Join(directory, entry.Name()), data, 0600); err != nil {
			return err
		}
	}

	config := "env \"dealfox\" {\n  url = getenv(\"DEALFOX_MIGRATION_URL\")\n  migration {\n    dir = \"file://" + filepath.ToSlash(directory) + "\"\n  }\n}\n"
	configPath := filepath.Join(directory, "atlas.hcl")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "atlas", "migrate", "apply", "--config", "file://"+filepath.ToSlash(configPath), "--env", "dealfox")
	cmd.Env = append(os.Environ(), "DEALFOX_MIGRATION_URL="+dsn)
	data, err := cmd.CombinedOutput()
	if err != nil {
		// Atlas connection diagnostics can include URLs. Keep credentials out
		// of the application error path and process argument list.
		return errors.New("atlas migration failed; verify Atlas is installed, database access, and migration state")
	}

	_, err = fmt.Fprint(output, strings.ReplaceAll(string(data), dsn, "[DATABASE_URL]"))

	return err
}
