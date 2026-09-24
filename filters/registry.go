package filters

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fishman/clashpulse/config"
	resourcepkg "github.com/fishman/clashpulse/resources"
)

// Provider is a Mihomo-compatible local rule provider derived from one
// resource-registry entry; filters never fetch or maintain their own files.
type Provider struct {
	Name     string
	ID       string
	Path     string
	Behavior string
	Format   string
}

type Reference struct {
	FilterID   string
	ResourceID string
	Provider   string
	Target     string
}

type Registry struct {
	providers  []Provider
	byID       map[string]Provider
	references []Reference
	byFilter   map[string]Reference
}

// Build binds each enabled rule resource and filter reference to validated
// managed paths from a resource plan or active resource registry.
func Build(snapshot config.Snapshot, managedPaths map[string]string) (*Registry, error) {
	registry := &Registry{byID: make(map[string]Provider), byFilter: make(map[string]Reference)}
	resources := make(map[string]config.Resource, len(snapshot.Resources))
	managedHome := ""
	for _, resource := range snapshot.Resources {
		if !validID(resource.ID) {
			return nil, fmt.Errorf("filters: invalid resource ID %q", resource.ID)
		}
		if _, exists := resources[resource.ID]; exists {
			return nil, fmt.Errorf("filters: duplicate resource ID %q", resource.ID)
		}
		resources[resource.ID] = resource
		if !resource.Enabled || resource.Kind != config.ResourceRuleProvider && resource.Kind != config.ResourceRuleSet {
			continue
		}
		if err := resourcepkg.ValidateDeclaration(resource); err != nil {
			return nil, fmt.Errorf("filters: resource %q has invalid declaration: %w", resource.ID, err)
		}
		path := managedPaths[resource.ID]
		if path == "" || !filepath.IsAbs(path) || filepath.Base(path) != ManagedFilename(resource) {
			return nil, fmt.Errorf("filters: resource %q has no registry destination", resource.ID)
		}
		if managedHome == "" {
			managedHome = filepath.Dir(path)
		} else if filepath.Dir(path) != managedHome {
			return nil, fmt.Errorf("filters: rule resources do not share one managed home")
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("filters: resource %q path is not a regular managed file", resource.ID)
		}
		provider := Provider{Name: Name(resource.ID), ID: resource.ID, Path: path, Behavior: string(resource.RuleType), Format: string(resource.Format)}
		registry.byID[resource.ID] = provider
		registry.providers = append(registry.providers, provider)
	}
	seenFilters := make(map[string]struct{}, len(snapshot.Filters))
	for _, filter := range snapshot.Filters {
		if !validID(filter.ID) {
			return nil, fmt.Errorf("filters: invalid filter ID %q", filter.ID)
		}
		if _, exists := seenFilters[filter.ID]; exists {
			return nil, fmt.Errorf("filters: duplicate filter ID %q", filter.ID)
		}
		seenFilters[filter.ID] = struct{}{}
		if !validTarget(filter.Target) {
			return nil, fmt.Errorf("filters: filter %q has invalid target", filter.ID)
		}
		if !filter.Enabled {
			continue
		}
		resource := resources[filter.Resource]
		provider, ok := registry.byID[filter.Resource]
		if !ok || filter.Format != resource.Format {
			return nil, fmt.Errorf("filters: filter %q requires an enabled compatible rule resource", filter.ID)
		}
		reference := Reference{FilterID: filter.ID, ResourceID: resource.ID, Provider: provider.Name, Target: filter.Target}
		registry.byFilter[filter.ID] = reference
		registry.references = append(registry.references, reference)
	}
	sort.Slice(registry.providers, func(i, j int) bool { return registry.providers[i].Name < registry.providers[j].Name })
	return registry, nil
}

// Name deterministically derives a Mihomo provider name from a stable resource
// ID. The ID is included verbatim only after it passes the narrow identifier
// grammar used by config validation.
func Name(resourceID string) string {
	if !validID(resourceID) {
		return ""
	}
	return "managed-" + resourceID
}

// Providers returns a stable sorted copy for deterministic rendering.
func (r *Registry) Providers() []Provider {
	if r == nil || len(r.providers) == 0 {
		return nil
	}
	return append([]Provider(nil), r.providers...)
}

// References returns the enabled filter rules in configuration order.
func (r *Registry) References() []Reference {
	if r == nil || len(r.references) == 0 {
		return nil
	}
	return append([]Reference(nil), r.references...)
}

// RuleSetReference returns a Mihomo RULE-SET rule with the filter's target.
func (r *Registry) RuleSetReference(filterID string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("filters: nil registry")
	}
	reference, ok := r.byFilter[filterID]
	if !ok {
		return "", fmt.Errorf("filters: filter %q is not enabled", filterID)
	}
	return "RULE-SET," + reference.Provider + "," + reference.Target, nil
}

// DNSRuleSetKey returns the deterministic nameserver-policy key for an enabled
// domain rule-set referenced by a filter.
func (r *Registry) DNSRuleSetKey(resourceID string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("filters: nil registry")
	}
	provider, ok := r.byID[resourceID]
	if !ok || provider.Behavior != string(config.RuleDomain) {
		return "", fmt.Errorf("filters: resource %q is not an enabled domain rule-set", resourceID)
	}
	return "rule-set:" + provider.Name, nil
}

// ManagedFilename returns the registry-controlled filename for a rule resource.
func ManagedFilename(resource config.Resource) string {
	return resourcepkg.ManagedFilename(resource)
}

func validTarget(target string) bool {
	return target != "" && strings.TrimSpace(target) == target && !strings.ContainsAny(target, ",\r\n\t\x00")
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 64 || id == "." || id == ".." {
		return false
	}
	for i, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_' || r == '.')) {
			continue
		}
		return false
	}
	return !strings.HasPrefix(id, ".")
}
