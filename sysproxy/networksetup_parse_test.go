package sysproxy

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDefaultInterface(t *testing.T) {
	got, err := parseDefaultInterface("   route to: default\n destination: default\n interface: en0\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "en0" {
		t.Fatalf("interface = %q, want en0", got)
	}
	for _, output := range []string{"route to: default\n", "interface: \n", "interface: en0\ninterface: en1\n"} {
		if _, err := parseDefaultInterface(output); err == nil {
			t.Errorf("parseDefaultInterface(%q) error = nil", output)
		}
	}
}

func TestParseNetworkServiceMapsDefaultDeviceToEnabledService(t *testing.T) {
	output := `An asterisk (*) denotes that a network service is disabled.
(1) *USB Ethernet
(Hardware Port: USB 10/100/1000 LAN, Device: en5)
(2) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)
(3) *Old Wi-Fi
(Hardware Port: Wi-Fi, Device: en1)
`
	for device, want := range map[string]string{"en0": "Wi-Fi", "en5": ""} {
		got, err := parseNetworkService(output, device)
		if want == "" {
			if err == nil {
				t.Errorf("parseNetworkService(%q) = %q, want disabled-service error", device, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("parseNetworkService(%q) = %q, %v; want %q", device, got, err, want)
		}
	}
}

func TestParseNetworkServiceRejectsAmbiguousDevice(t *testing.T) {
	output := `(1) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)
(2) Wi-Fi backup
(Hardware Port: USB Ethernet, Device: en0)
`
	if _, err := parseNetworkService(output, "en0"); err == nil || !strings.Contains(err.Error(), "multiple enabled services") {
		t.Fatalf("parseNetworkService() error = %v, want ambiguity error", err)
	}
}

func TestParseNetworkProxy(t *testing.T) {
	got, err := parseNetworkProxy("Enabled: Yes\nServer: proxy.example\nPort: 8080\nAuthenticated Proxy Enabled: 0\n")
	if err != nil {
		t.Fatal(err)
	}
	want := networkProxySettings{enabled: true, host: "proxy.example", port: 8080}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}

	got, err = parseNetworkProxy("Enabled: No\nServer: proxy.example\nPort: 3128\nAuthenticated Proxy Enabled: Yes\n")
	if err != nil {
		t.Fatal(err)
	}
	if !got.auth || got.enabled {
		t.Fatalf("parsed authenticated disabled proxy = %+v", got)
	}
	for _, output := range []string{
		"Enabled: Maybe\nServer: proxy\nPort: 8080\nAuthenticated Proxy Enabled: 0\n",
		"Enabled: Yes\nServer: proxy\nPort: 70000\nAuthenticated Proxy Enabled: 0\n",
		"Enabled: Yes\nServer: proxy\nAuthenticated Proxy Enabled: 0\n",
		"Enabled: Yes\nServer: \nPort: 8080\nAuthenticated Proxy Enabled: 0\n",
	} {
		if _, err := parseNetworkProxy(output); err == nil {
			t.Errorf("parseNetworkProxy(%q) error = nil", output)
		}
	}
}

func TestParsePACAndAutodiscoveryState(t *testing.T) {
	pac, err := parsePACEnabled("URL: http://proxy.example/proxy.pac\nEnabled: No\n")
	if err != nil || pac {
		t.Fatalf("parsePACEnabled() = %t, %v; want false", pac, err)
	}
	pac, err = parsePACEnabled("URL: http://proxy.example/proxy.pac\nEnabled: Yes\n")
	if err != nil || !pac {
		t.Fatalf("parsePACEnabled() = %t, %v; want true", pac, err)
	}
	auto, err := parseAutoDiscoveryEnabled("Auto Proxy Discovery: Off\n")
	if err != nil || auto {
		t.Fatalf("parseAutoDiscoveryEnabled() = %t, %v; want false", auto, err)
	}
	auto, err = parseAutoDiscoveryEnabled("Auto Proxy Discovery: On\n")
	if err != nil || !auto {
		t.Fatalf("parseAutoDiscoveryEnabled() = %t, %v; want true", auto, err)
	}
	auto, err = parseAutoDiscoveryEnabled("Proxy Auto Discovery: On\n")
	if err != nil || !auto {
		t.Fatalf("parseAutoDiscoveryEnabled() = %t, %v; want true", auto, err)
	}
	auto, err = parseAutoDiscoveryEnabled("Proxy Auto Discover: Off\n")
	if err != nil || auto {
		t.Fatalf("parseAutoDiscoveryEnabled() = %t, %v; want false", auto, err)
	}
	if _, err := parsePACEnabled("URL: \n"); err == nil {
		t.Fatal("parsePACEnabled() accepted output with missing Enabled field")
	}
	if _, err := parseAutoDiscoveryEnabled("Auto Proxy Discovery: Maybe\n"); err == nil {
		t.Fatal("parseAutoDiscoveryEnabled() accepted an unknown state")
	}
}
