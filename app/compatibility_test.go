package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/mihomo"
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

func TestActivationCapabilityNamesManagedResource(t *testing.T) {
	home := t.TempDir()
	service := &runtimeService{controllerAddress: "127.0.0.1:9090", secret: "test-secret"}
	intent := config.Snapshot{
		DNS:       config.DNS{Listen: "127.0.0.1:1053"},
		Resources: []config.Resource{{ID: "geosite", Kind: config.ResourceGeoSite, Format: config.FormatDAT, Enabled: true, URL: "https://private.invalid/?token=private"}},
	}
	_, err := service.renderWithHome(context.Background(), []byte("proxies:\n  - name: alpha\n    type: direct\n"), intent, home,
		map[string]string{"geosite": filepath.Join(home, "geosite.dat")}, mihomo.Capability{})
	public, ok := core.PublicActivation(err)
	if !ok || public.Stage != core.ActivationBinary || public.ResourceID != "geosite" ||
		!strings.Contains(public.Error(), "geosite") || strings.Contains(public.Error(), "private") || strings.Contains(public.Error(), "://") {
		t.Fatalf("capability failure lost resource or leaked source: %v", err)
	}
}
