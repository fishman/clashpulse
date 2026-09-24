package app

import (
	"fmt"
	"time"

	"github.com/gen2brain/beeep"
)

// notifySlowConnections emits only aggregate latency, never proxy names or URLs.
func notifySlowConnections(threshold time.Duration) error {
	beeep.AppName = "ClashPulse"
	return beeep.Notify("ClashPulse connection health", fmt.Sprintf("All measured connections are slower than %d ms", threshold.Milliseconds()), "")
}
