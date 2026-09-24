package examples

import "embed"

// Files is the single source for annotated first-run configuration.
//
//go:embed *.toml
var Files embed.FS
