package migrations

import "embed"

//go:embed *.sql *.json
var Files embed.FS
