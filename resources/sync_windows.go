//go:build windows

package resources

// Windows cannot flush a directory opened through os.Open. Resource files and
// the journal are synced before same-volume renames; directory flush is not available.
func syncDirectory(string) error { return nil }
