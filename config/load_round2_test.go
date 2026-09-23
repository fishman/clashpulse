package config

import (
	"strings"
	"testing"
)

func TestLoadAcceptsDottedTablesAndMultilineStrings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", strings.Join([]string{
		"mihomo.binary = \"system\"",
		"monitor.enabled = true",
		"monitor.test_url = '''https://example.invalid/generate_204'''",
		"monitor.interval = \"5m\"",
		"dns.listen = \"127.0.0.1:53\"",
		"system_proxy.enabled = true",
	}, "\n"))
	writeFile(t, dir, "subscriptions.toml", strings.Join([]string{
		"[[subscription]]",
		"id = \"primary\"",
		"name = \"\"\"Primary\nProfile\"\"\"",
		"url = \"https://provider.example/subscription\"",
		"enabled = true",
		"refresh_interval = \"12h\"",
	}, "\n"))
	if _, err := Load(dir); err != nil {
		t.Fatalf("dotted TOML rejected: %v", err)
	}
}
