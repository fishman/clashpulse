package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompatibilityFailureIdentifiesResourceWithoutSourceURL(t *testing.T) {
	issue := fmt.Errorf("mihomo: resource %q requires geoip.dat capability; source https://provider.example/path?token=secret", "geo")
	got := compatibilityFailure(issue)
	if !strings.Contains(got, "geo") || !strings.Contains(got, "geoip.dat") || strings.Contains(got, "secret") || strings.Contains(got, "provider.example") {
		t.Fatalf("compatibility result = %q", got)
	}
	if got := compatibilityFailure(fmt.Errorf("clashpulse: bundled Mihomo is not installed")); got != "bundled Mihomo is not installed" {
		t.Fatalf("bundle availability = %q", got)
	}
}
