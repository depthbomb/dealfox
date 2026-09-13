module github.com/depthbomb/dealfox

go 1.27.1

require (
	github.com/depthbomb/argon v0.0.0-20260912222847-8f94aca9a093
	github.com/depthbomb/cuid2 v0.1.0
	github.com/depthbomb/envschema v0.1.0
	github.com/depthbomb/nook v0.0.0-20260912233528-6193cda83a56
	github.com/depthbomb/tomogo v0.0.0-20260909055127-3b2c6cd319e0
	github.com/depthbomb/tomogo/adapters/embedbuilder v0.0.0-20260909055127-3b2c6cd319e0
	github.com/gofrs/flock v0.13.0
	github.com/tomogo-framework/embed-builder v0.1.1
	github.com/tomogo-framework/snowflake v0.1.0
	golang.org/x/net v0.59.0
	golang.org/x/sync v0.23.0
	modernc.org/sqlite v1.58.0
)

require (
	github.com/depthbomb/duration v1.0.2 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/telemetry v0.0.0-20260910141331-15ceca2b0a1f // indirect
	golang.org/x/tools v0.50.0 // indirect
	modernc.org/libc v1.75.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

tool (
	github.com/depthbomb/envschema/cmd/envschema
	github.com/depthbomb/nook/cmd/nook
	golang.org/x/tools/cmd/goimports
)
