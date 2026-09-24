package ui

import (
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
)

func TestBinarySummaryDisplaysVerifiedCapabilities(t *testing.T) {
	detail := binarySummary(core.BinarySnapshot{Desired: "system", ObservedVersion: "v1.19.31", Capabilities: []string{"geoip.dat", "Country.mmdb"}})
	if !strings.Contains(detail, "geoip.dat") || !strings.Contains(detail, "Country.mmdb") {
		t.Fatalf("verified capabilities hidden: %q", detail)
	}
}
