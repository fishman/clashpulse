// Package bundled records the Mihomo executable a release package ships.
// The release workflow sets both values with -ldflags -X; every other build,
// including Homebrew and source builds, leaves them empty and has no bundle.
package bundled

var (
	// Version is the upstream Mihomo release tag, for example v1.19.34.
	Version string
	// SHA256 is the hex digest of the packaged executable, not its archive.
	SHA256 string
)

func Available() bool { return SHA256 != "" }
