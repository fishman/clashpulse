package config

// ReplaceDNSRouting replaces the declarative resolver and route tables in
// resources.toml without touching its managed resource source declarations.
func ReplaceDNSRouting(path string, current Snapshot, sets []ResolverSet, routes []DNSRoute) error {
	var document resourcesDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	next := cloneSnapshot(current)
	next.DNS.ResolverSets = cloneResolverSets(sets)
	next.DNS.Routes = cloneDNSRoutes(routes)
	if err := validateSnapshot(next); err != nil {
		return err
	}
	document.ResolverSets = make([]resolverSetDoc, 0, len(sets))
	for _, set := range sets {
		document.ResolverSets = append(document.ResolverSets, resolverSetDoc{ID: set.ID, Endpoints: append([]string(nil), set.Endpoints...), DNSCrypt: set.DNSCrypt})
	}
	document.Routes = make([]dnsRouteDoc, 0, len(routes))
	for _, route := range routes {
		document.Routes = append(document.Routes, dnsRouteDoc{Suffix: route.Suffix, GeoSite: route.GeoSite, Resource: route.Resource, ResolverSet: route.ResolverSet})
	}
	return writeResources(path, document)
}
