package migrations

import (
	"embed"
	"fmt"
	"strings"

	"ariga.io/atlas/sql/migrate"
)

//go:embed *.sql atlas.sum
var Files embed.FS

func Hashes() (map[string]string, error) {
	entries, err := Files.ReadDir(".")
	if err != nil {
		return nil, err
	}

	var files []migrate.File
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		data, err := Files.ReadFile(entry.Name())
		if err != nil {
			return nil, err
		}
		files = append(files, migrate.NewLocalFile(entry.Name(), data))
	}

	actual, err := migrate.NewHashFile(files)
	if err != nil {
		return nil, err
	}

	data, err := Files.ReadFile("atlas.sum")
	if err != nil {
		return nil, err
	}

	var expected migrate.HashFile
	if err := expected.UnmarshalText(data); err != nil {
		return nil, err
	}

	if expected.Sum() != actual.Sum() {
		return nil, fmt.Errorf("embedded migrations do not match atlas.sum")
	}

	hashes := make(map[string]string, len(actual))
	for _, file := range actual {
		version, _, _ := strings.Cut(file.N, "_")
		hashes[version] = file.H
	}

	return hashes, nil
}
