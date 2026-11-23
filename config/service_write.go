package config

import (
	"fmt"
	"slices"

	"github.com/fishman/clashpulse/services"
)

func validateServices(s Snapshot) error {
	seen := make(map[string]bool, len(s.Services))
	for i, service := range s.Services {
		key := fmt.Sprintf("config.toml: service[%d]", i)
		if _, ok := services.Lookup(service.ID); !ok {
			return fmt.Errorf("%s.id: unknown service %q", key, service.ID)
		}
		if seen[service.ID] {
			return fmt.Errorf("%s.id: duplicate service %q", key, service.ID)
		}
		seen[service.ID] = true
		for _, region := range service.Regions {
			if !ValidRegion(region) {
				return fmt.Errorf("%s.regions: %q is not an upper-case two-letter country code", key, region)
			}
		}
		if service.CheckInterval < MinServiceInterval {
			return fmt.Errorf("%s.check_interval: must be at least %s", key, MinServiceInterval)
		}
		if service.Failures < 1 || service.Failures > MaxServiceFailures {
			return fmt.Errorf("%s.failures: must be between 1 and %d", key, MaxServiceFailures)
		}
		if service.DomainsResource != "" {
			index := slices.IndexFunc(s.Resources, func(r Resource) bool { return r.ID == service.DomainsResource })
			if index < 0 {
				return fmt.Errorf("%s.domains_resource: unknown resource %q", key, service.DomainsResource)
			}
			resource := s.Resources[index]
			if !resource.Enabled || resource.Kind != ResourceRuleSet && resource.Kind != ResourceRuleProvider || resource.RuleType != RuleDomain {
				return fmt.Errorf("%s.domains_resource: %q must be an enabled domain rule-set resource", key, service.DomainsResource)
			}
		}
	}
	return nil
}

// ValidRegion reports whether code is an upper-case ISO 3166-1 alpha-2 code.
func ValidRegion(code string) bool {
	return len(code) == 2 && code[0] >= 'A' && code[0] <= 'Z' && code[1] >= 'A' && code[1] <= 'Z'
}

// PutService adds or replaces one [[service]] entry in config.toml, leaving
// every other table as written.
func PutService(path string, current Snapshot, service Service) error {
	var document configDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	next := cloneSnapshot(current)
	index := slices.IndexFunc(next.Services, func(s Service) bool { return s.ID == service.ID })
	if index >= 0 {
		next.Services[index] = service
	} else {
		next.Services = append(next.Services, service)
	}
	if err := validateSnapshot(next); err != nil {
		return err
	}
	interval, failures := service.CheckInterval.String(), service.Failures
	entry := serviceDoc{ID: service.ID, Enabled: service.Enabled, Regions: append([]string(nil), service.Regions...),
		CheckInterval: &interval, Failures: &failures, Automatic: service.Automatic, DomainsResource: service.DomainsResource}
	index = slices.IndexFunc(document.Services, func(doc serviceDoc) bool { return doc.ID == service.ID })
	if index >= 0 {
		document.Services[index] = entry
	} else {
		document.Services = append(document.Services, entry)
	}
	return writeTOML(path, document)
}
