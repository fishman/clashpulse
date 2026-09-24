package tui

import "testing"

func TestResolverRenameRetargetsExistingDNSRoutes(t *testing.T) {
	model := dnsPolicyModel(t)
	model.Selection[TabSettings] = "dns:set:one"
	model, _, _ = model.HandleKey("g")
	model, _ = replaceModalField(t, model, "renamed")
	model, _, _ = model.HandleKey("enter")        // Keep the existing resolver endpoint.
	model, command, _ := model.HandleKey("enter") // Keep the DNSCrypt setting.
	if command == nil || command.DNSRouting == nil || command.DNSRouting.ResolverSets[0].ID != "renamed" || command.DNSRouting.Routes[0].ResolverSet != "renamed" {
		t.Fatalf("resolver rename left a dangling DNS route: %#v", command)
	}
	if model.snapshot.Snapshot.DNS.ResolverSets[0].ID != "one" {
		t.Fatal("selection changed before snapshot")
	}
}
