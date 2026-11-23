package mihomo

import (
	"fmt"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/filters"
	"github.com/fishman/clashpulse/services"
)

const serviceGroupPrefix = "ClashPulse - "

// ServiceGroupName is the select group routing a service's domains.
func ServiceGroupName(name string) string { return serviceGroupPrefix + name }

// ServiceGroupFor maps a group name back to its service ID.
func ServiceGroupFor(groupName string) (string, bool) {
	for _, def := range services.Definitions() {
		if ServiceGroupName(def.Name) == groupName {
			return def.ID, true
		}
	}
	return "", false
}

// renderServices adds one select group per enabled service and its rules ahead
// of the profile's rules; renderFilters later prepends filter rules ahead of these.
func renderServices(document map[string]any, intent config.Snapshot) error {
	enabled := make(map[string]config.Service, len(intent.Services))
	for _, service := range intent.Services {
		if service.Enabled {
			enabled[service.ID] = service
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	groups, ok := document["proxy-groups"].([]any)
	if !ok && document["proxy-groups"] != nil {
		return fmt.Errorf("mihomo: source proxy-groups must be a list")
	}
	sourceRules, ok := document["rules"].([]any)
	if !ok && document["rules"] != nil {
		return fmt.Errorf("mihomo: source rules must be a list")
	}
	existing := make(map[string]bool)
	for _, section := range []string{"proxies", "proxy-groups"} {
		items, _ := document[section].([]any)
		for _, item := range items {
			if entry, ok := item.(map[string]any); ok {
				if name, ok := entry["name"].(string); ok {
					existing[name] = true
				}
			}
		}
	}
	var rules []any
	for _, def := range services.Definitions() {
		service, ok := enabled[def.ID]
		if !ok {
			continue
		}
		name := ServiceGroupName(def.Name)
		if existing[name] {
			return fmt.Errorf("mihomo: source profile already defines %q, the group for service %s", name, def.ID)
		}
		groups = append(groups, map[string]any{"name": name, "type": "select", "include-all": true})
		if service.DomainsResource != "" {
			rules = append(rules, "RULE-SET,"+filters.Name(service.DomainsResource)+","+name)
			continue
		}
		for _, domain := range def.Domains {
			rules = append(rules, "DOMAIN-SUFFIX,"+domain+","+name)
		}
	}
	document["proxy-groups"] = groups
	document["rules"] = append(rules, sourceRules...)
	return nil
}
