//go:build windows

package subscriptions

// Windows cannot flush a directory opened through os.Open: FlushFileBuffers
// needs write access the handle lacks, so a deletion would never complete.
// Files are synced before same-volume renames; directory flush is not available.
func syncDirectory(string) error { return nil }
