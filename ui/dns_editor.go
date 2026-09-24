package ui

import (
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

type dnsSettings struct {
	view            fyne.CanvasObject
	send            sendIntent
	window          fyne.Window
	snapshot        core.DNSSnapshot
	refreshing      bool
	listener        *widget.Label
	resolverSummary *widget.Label
	routeSummary    *widget.Label
	resolverSelect  *widget.Select
	routeSelect     *widget.Select
	routeIndexes    map[string]int
	editListener    *widget.Button
	addResolver     *widget.Button
	editResolver    *widget.Button
	removeResolver  *widget.Button
	addRoute        *widget.Button
	editRoute       *widget.Button
	removeRoute     *widget.Button
	listenerEditor  *widget.Entry
	listenerDialog  *dialog.FormDialog
	resolverEditor  *dnsResolverEditor
	routeEditor     *dnsRouteEditor
}

func newDNSSettings(send sendIntent, window fyne.Window) *dnsSettings {
	p := &dnsSettings{send: send, window: window, routeIndexes: make(map[string]int)}
	p.listener = widget.NewLabel("Listen: not configured")
	p.resolverSummary = widget.NewLabel("No resolver sets configured")
	p.resolverSummary.Wrapping = fyne.TextWrapWord
	p.routeSummary = widget.NewLabel("No DNS routes configured")
	p.routeSummary.Wrapping = fyne.TextWrapWord
	p.editListener = widget.NewButton("Change listener", p.openListenerEditor)
	p.resolverSelect = widget.NewSelect(nil, func(string) {
		if !p.refreshing {
			p.updateButtons()
		}
	})
	p.resolverSelect.PlaceHolder = "Select resolver set"
	p.routeSelect = widget.NewSelect(nil, func(string) {
		if !p.refreshing {
			p.updateButtons()
		}
	})
	p.routeSelect.PlaceHolder = "Select DNS route"
	p.addResolver = widget.NewButton("Add resolver set", func() { p.openResolverEditor(nil) })
	p.editResolver = widget.NewButton("Edit resolver set", p.editSelectedResolver)
	p.removeResolver = widget.NewButton("Remove resolver set", p.removeSelectedResolver)
	p.addRoute = widget.NewButton("Add DNS route", func() { p.openRouteEditor(-1) })
	p.editRoute = widget.NewButton("Edit DNS route", p.editSelectedRoute)
	p.removeRoute = widget.NewButton("Remove DNS route", p.removeSelectedRoute)
	p.view = container.NewVBox(
		widget.NewLabelWithStyle("DNS policy", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(widget.NewLabel("DNS listener"), p.listener, p.editListener),
		widget.NewLabelWithStyle("Resolver sets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		p.resolverSummary,
		p.resolverSelect,
		container.NewHBox(p.addResolver, p.editResolver, p.removeResolver),
		widget.NewLabelWithStyle("DNS routes", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		p.routeSummary,
		p.routeSelect,
		container.NewHBox(p.addRoute, p.editRoute, p.removeRoute),
	)
	p.updateButtons()
	return p
}

func (p *dnsSettings) update(snapshot core.DNSSnapshot) {
	listenerChanged := p.snapshot.Listen != snapshot.Listen
	resolversChanged := !reflect.DeepEqual(p.snapshot.ResolverSets, snapshot.ResolverSets)
	routesChanged := !reflect.DeepEqual(p.snapshot.Routes, snapshot.Routes)
	if !listenerChanged && !resolversChanged && !routesChanged {
		return
	}
	selectedResolver := p.resolverSelect.Selected
	var selectedRoute *core.DNSRouteSnapshot
	if index, exists := p.routeIndexes[p.routeSelect.Selected]; exists && index >= 0 && index < len(p.snapshot.Routes) {
		route := p.snapshot.Routes[index]
		selectedRoute = &route
	}
	p.snapshot = cloneDNSSnapshot(snapshot)
	if listenerChanged {
		p.listener.SetText("Listen: " + dnsListenLabel(snapshot.Listen))
	}
	p.refreshing = true
	if resolversChanged {
		lines := make([]string, 0, len(snapshot.ResolverSets))
		p.resolverSelect.Options = make([]string, 0, len(snapshot.ResolverSets))
		for _, set := range snapshot.ResolverSets {
			endpoints := make([]string, len(set.Endpoints))
			for i, endpoint := range set.Endpoints {
				endpoints[i] = dnsEndpointLabel(endpoint)
			}
			detail := set.ID + ": " + strings.Join(endpoints, ", ")
			if set.DNSCrypt {
				detail += " (DNSCrypt)"
			}
			lines = append(lines, detail)
			p.resolverSelect.Options = append(p.resolverSelect.Options, set.ID)
		}
		if len(lines) == 0 {
			p.resolverSummary.SetText("No resolver sets configured")
		} else {
			p.resolverSummary.SetText(strings.Join(lines, "\n"))
		}
		p.resolverSelect.Selected = selectedResolver
		if !containsString(p.resolverSelect.Options, selectedResolver) {
			p.resolverSelect.Selected = ""
		}
		p.resolverSelect.Refresh()
	}
	if routesChanged {
		lines := make([]string, 0, len(snapshot.Routes))
		p.routeIndexes = make(map[string]int, len(snapshot.Routes))
		p.routeSelect.Options = make([]string, 0, len(snapshot.Routes))
		for i, route := range snapshot.Routes {
			label := dnsRouteDetail(route)
			lines = append(lines, label)
			option := fmt.Sprintf("%d. %s", i+1, label)
			p.routeSelect.Options = append(p.routeSelect.Options, option)
			p.routeIndexes[option] = i
		}
		if len(lines) == 0 {
			p.routeSummary.SetText("No DNS routes configured")
		} else {
			p.routeSummary.SetText(strings.Join(lines, "\n"))
		}
		p.routeSelect.Selected = ""
		if selectedRoute != nil {
			for i, route := range snapshot.Routes {
				if route == *selectedRoute {
					p.routeSelect.Selected = fmt.Sprintf("%d. %s", i+1, dnsRouteDetail(route))
					break
				}
			}
		}
		p.routeSelect.Refresh()
	}
	p.refreshing = false
	p.updateButtons()
}

func (p *dnsSettings) updateButtons() {
	if p == nil || p.resolverSelect == nil || p.routeSelect == nil {
		return
	}
	if p.resolverSelect.Selected != "" && findResolverSet(p.snapshot, p.resolverSelect.Selected) >= 0 {
		p.editResolver.Enable()
		p.removeResolver.Enable()
	} else {
		p.editResolver.Disable()
		p.removeResolver.Disable()
	}
	if _, exists := p.routeIndexes[p.routeSelect.Selected]; p.routeSelect.Selected != "" && exists {
		p.editRoute.Enable()
		p.removeRoute.Enable()
	} else {
		p.editRoute.Disable()
		p.removeRoute.Disable()
	}
}

func (p *dnsSettings) openListenerEditor() {
	input := widget.NewEntry()
	input.SetText(p.snapshot.Listen)
	input.SetPlaceHolder("127.0.0.1:5353 or [::1]:5353")
	p.listenerEditor = input
	p.listenerDialog = dialog.NewForm("DNS listener", "Save", "Cancel", []*widget.FormItem{widget.NewFormItem("Loopback address", input)}, func(confirmed bool) {
		p.listenerDialog = nil
		if !confirmed {
			return
		}
		listen := strings.TrimSpace(input.Text)
		if !validDNSListen(listen) {
			dialog.ShowError(fmt.Errorf("DNS listener must use a loopback IP address and valid port"), p.window)
			return
		}
		if listen != p.snapshot.Listen {
			p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{DNSListen: &listen}})
		}
	}, p.window)
	p.listenerDialog.Show()
}

func (p *dnsSettings) editSelectedResolver() {
	index := findResolverSet(p.snapshot, p.resolverSelect.Selected)
	if index < 0 {
		return
	}
	set := cloneResolverSet(p.snapshot.ResolverSets[index])
	p.openResolverEditor(&set)
}

func (p *dnsSettings) openResolverEditor(existing *core.ResolverSetSnapshot) {
	var selected *core.ResolverSetSnapshot
	if existing != nil {
		copy := cloneResolverSet(*existing)
		selected = &copy
	}
	e := &dnsResolverEditor{existingID: ""}
	if selected != nil {
		e.existingID = selected.ID
	}
	e.id = widget.NewEntry()
	e.endpoints = widget.NewMultiLineEntry()
	e.dnscrypt = widget.NewCheck("DNSCrypt listener", nil)
	e.id.SetPlaceHolder("Stable resolver set ID")
	items := make([]*widget.FormItem, 0, 3)
	if selected == nil {
		items = append(items, widget.NewFormItem("Stable ID", e.id))
	} else {
		e.id.SetText(selected.ID)
		e.endpoints.SetText(strings.Join(selected.Endpoints, "\n"))
		e.dnscrypt.SetChecked(selected.DNSCrypt)
		items = append(items, widget.NewFormItem("Stable ID", e.id))
	}
	e.endpoints.SetPlaceHolder("One DNS endpoint per line")
	items = append(items, widget.NewFormItem("Endpoints", e.endpoints), widget.NewFormItem("", e.dnscrypt))
	p.resolverEditor = e
	title, confirm := "Add resolver set", "Add"
	if selected != nil {
		title, confirm = "Edit resolver set", "Save"
	}
	e.dialog = dialog.NewForm(title, confirm, "Cancel", items, func(confirmed bool) {
		p.resolverEditor = nil
		if !confirmed {
			return
		}
		command, changed, err := e.command(p.snapshot)
		if err != nil {
			dialog.ShowError(err, p.window)
			return
		}
		if changed {
			p.send(command)
		}
	}, p.window)
	e.dialog.Show()
}

func (p *dnsSettings) removeSelectedResolver() {
	id := p.resolverSelect.Selected
	index := findResolverSet(p.snapshot, id)
	if index < 0 {
		return
	}
	for _, route := range p.snapshot.Routes {
		if route.ResolverSet == id {
			dialog.ShowError(fmt.Errorf("remove or reassign DNS routes using resolver set %q first", id), p.window)
			return
		}
	}
	edit := dnsRoutingEdit(p.snapshot)
	edit.ResolverSets = append(edit.ResolverSets[:index], edit.ResolverSets[index+1:]...)
	p.send(ipc.Command{Kind: ipc.CommandSetDNSRouting, DNSRouting: edit})
}

func (p *dnsSettings) editSelectedRoute() {
	index, exists := p.routeIndexes[p.routeSelect.Selected]
	if !exists || index < 0 || index >= len(p.snapshot.Routes) {
		return
	}
	p.openRouteEditor(index)
}

func (p *dnsSettings) openRouteEditor(index int) {
	var existing *core.DNSRouteSnapshot
	if index >= 0 && index < len(p.snapshot.Routes) {
		copy := p.snapshot.Routes[index]
		existing = &copy
	}
	e := &dnsRouteEditor{existing: existing, existingIndex: index}
	e.matcher = widget.NewSelect([]string{"Domain suffix", "GeoSite", "Resource ID"}, nil)
	e.resolver = widget.NewSelect(nil, nil)
	e.value = widget.NewEntry()
	e.value.SetPlaceHolder("Matcher value")
	e.resolver.Options = make([]string, 0, len(p.snapshot.ResolverSets))
	for _, set := range p.snapshot.ResolverSets {
		e.resolver.Options = append(e.resolver.Options, set.ID)
	}
	e.resolver.Refresh()
	if existing != nil {
		switch {
		case existing.Suffix != "":
			e.matcher.SetSelected("Domain suffix")
			e.value.SetText(existing.Suffix)
		case existing.GeoSite != "":
			e.matcher.SetSelected("GeoSite")
			e.value.SetText(existing.GeoSite)
		case existing.Resource != "":
			e.matcher.SetSelected("Resource ID")
			e.value.SetText(existing.Resource)
		}
		e.resolver.SetSelected(existing.ResolverSet)
	}
	p.routeEditor = e
	title, confirm := "Add DNS route", "Add"
	if existing != nil {
		title, confirm = "Edit DNS route", "Save"
	}
	e.dialog = dialog.NewForm(title, confirm, "Cancel", []*widget.FormItem{
		widget.NewFormItem("Matcher type", e.matcher),
		widget.NewFormItem("Matcher value", e.value),
		widget.NewFormItem("Resolver set", e.resolver),
	}, func(confirmed bool) {
		p.routeEditor = nil
		if !confirmed {
			return
		}
		command, changed, err := e.command(p.snapshot)
		if err != nil {
			dialog.ShowError(err, p.window)
			return
		}
		if changed {
			p.send(command)
		}
	}, p.window)
	e.dialog.Show()
}

func (p *dnsSettings) removeSelectedRoute() {
	index, exists := p.routeIndexes[p.routeSelect.Selected]
	if !exists || index < 0 || index >= len(p.snapshot.Routes) {
		return
	}
	edit := dnsRoutingEdit(p.snapshot)
	edit.Routes = append(edit.Routes[:index], edit.Routes[index+1:]...)
	p.send(ipc.Command{Kind: ipc.CommandSetDNSRouting, DNSRouting: edit})
}

type dnsResolverEditor struct {
	existingID string
	id         *widget.Entry
	endpoints  *widget.Entry
	dnscrypt   *widget.Check
	dialog     *dialog.FormDialog
}

func (e *dnsResolverEditor) command(current core.DNSSnapshot) (ipc.Command, bool, error) {
	id := strings.TrimSpace(e.id.Text)
	if !validSubscriptionID(id) {
		return ipc.Command{}, false, fmt.Errorf("resolver set ID must start with a letter or number and use only letters, numbers, '.', '_' or '-' (1-64 characters)")
	}
	index := findResolverSet(current, e.existingID)
	if e.existingID == "" {
		index = findResolverSet(current, id)
		if index >= 0 {
			return ipc.Command{}, false, fmt.Errorf("resolver set ID already exists")
		}
	} else if index < 0 {
		return ipc.Command{}, false, fmt.Errorf("resolver set no longer exists")
	} else if id != e.existingID && findResolverSet(current, id) >= 0 {
		return ipc.Command{}, false, fmt.Errorf("resolver set ID already exists")
	}
	endpoints := make([]string, 0, 4)
	for _, raw := range strings.Split(e.endpoints.Text, "\n") {
		endpoint := strings.TrimSpace(raw)
		if endpoint == "" {
			continue
		}
		if err := validateDNSEndpoint(endpoint, e.dnscrypt.Checked); err != nil {
			return ipc.Command{}, false, err
		}
		endpoints = append(endpoints, endpoint)
	}
	if len(endpoints) == 0 || len(endpoints) > 16 {
		return ipc.Command{}, false, fmt.Errorf("resolver set must contain 1-16 endpoints")
	}
	set := core.ResolverSetSnapshot{ID: id, Endpoints: endpoints, DNSCrypt: e.dnscrypt.Checked}
	if index >= 0 && reflect.DeepEqual(current.ResolverSets[index], set) {
		return ipc.Command{}, false, nil
	}
	edit := dnsRoutingEdit(current)
	if index < 0 {
		edit.ResolverSets = append(edit.ResolverSets, dnsResolverSetEdit(set))
	} else {
		edit.ResolverSets[index] = dnsResolverSetEdit(set)
		if id != e.existingID {
			for i := range edit.Routes {
				if edit.Routes[i].ResolverSet == e.existingID {
					edit.Routes[i].ResolverSet = id
				}
			}
		}
	}
	return ipc.Command{Kind: ipc.CommandSetDNSRouting, DNSRouting: edit}, true, nil
}

type dnsRouteEditor struct {
	existing      *core.DNSRouteSnapshot
	existingIndex int
	matcher       *widget.Select
	value         *widget.Entry
	resolver      *widget.Select
	dialog        *dialog.FormDialog
}

func (e *dnsRouteEditor) command(current core.DNSSnapshot) (ipc.Command, bool, error) {
	value := strings.TrimSpace(e.value.Text)
	resolverSet := strings.TrimSpace(e.resolver.Selected)
	if findResolverSet(current, resolverSet) < 0 {
		return ipc.Command{}, false, fmt.Errorf("select an existing resolver set")
	}
	route := core.DNSRouteSnapshot{ResolverSet: resolverSet}
	switch e.matcher.Selected {
	case "Domain suffix":
		if !validDNSDomain(value) {
			return ipc.Command{}, false, fmt.Errorf("domain suffix is invalid")
		}
		route.Suffix = value
	case "GeoSite":
		if !validDNSRuleToken(value) {
			return ipc.Command{}, false, fmt.Errorf("GeoSite selector is invalid")
		}
		route.GeoSite = value
	case "Resource ID":
		if !validSubscriptionID(value) {
			return ipc.Command{}, false, fmt.Errorf("resource ID is invalid")
		}
		route.Resource = value
	default:
		return ipc.Command{}, false, fmt.Errorf("select exactly one route matcher")
	}
	edit := dnsRoutingEdit(current)
	if e.existing == nil {
		edit.Routes = append(edit.Routes, ipc.DNSRoute{Suffix: route.Suffix, GeoSite: route.GeoSite, Resource: route.Resource, ResolverSet: route.ResolverSet})
		return ipc.Command{Kind: ipc.CommandSetDNSRouting, DNSRouting: edit}, true, nil
	}
	index := e.existingIndex
	if index < 0 || index >= len(current.Routes) || current.Routes[index] != *e.existing {
		index = findDNSRoute(current.Routes, *e.existing)
	}
	if index < 0 {
		return ipc.Command{}, false, fmt.Errorf("DNS route no longer exists")
	}
	if reflect.DeepEqual(current.Routes[index], route) {
		return ipc.Command{}, false, nil
	}
	edit.Routes[index] = ipc.DNSRoute{Suffix: route.Suffix, GeoSite: route.GeoSite, Resource: route.Resource, ResolverSet: route.ResolverSet}
	return ipc.Command{Kind: ipc.CommandSetDNSRouting, DNSRouting: edit}, true, nil
}

func dnsRoutingEdit(snapshot core.DNSSnapshot) *ipc.DNSRoutingEdit {
	edit := &ipc.DNSRoutingEdit{
		ResolverSets: make([]ipc.DNSResolverSet, 0, len(snapshot.ResolverSets)),
		Routes:       make([]ipc.DNSRoute, 0, len(snapshot.Routes)),
	}
	for _, set := range snapshot.ResolverSets {
		edit.ResolverSets = append(edit.ResolverSets, dnsResolverSetEdit(set))
	}
	for _, route := range snapshot.Routes {
		edit.Routes = append(edit.Routes, ipc.DNSRoute{Suffix: route.Suffix, GeoSite: route.GeoSite, Resource: route.Resource, ResolverSet: route.ResolverSet})
	}
	return edit
}

func dnsResolverSetEdit(set core.ResolverSetSnapshot) ipc.DNSResolverSet {
	return ipc.DNSResolverSet{ID: set.ID, Endpoints: append([]string(nil), set.Endpoints...), DNSCrypt: set.DNSCrypt}
}

func cloneDNSSnapshot(snapshot core.DNSSnapshot) core.DNSSnapshot {
	out := snapshot
	out.ResolverSets = make([]core.ResolverSetSnapshot, len(snapshot.ResolverSets))
	for i, set := range snapshot.ResolverSets {
		out.ResolverSets[i] = cloneResolverSet(set)
	}
	out.Routes = append([]core.DNSRouteSnapshot(nil), snapshot.Routes...)
	return out
}

func cloneResolverSet(set core.ResolverSetSnapshot) core.ResolverSetSnapshot {
	set.Endpoints = append([]string(nil), set.Endpoints...)
	return set
}

func findResolverSet(snapshot core.DNSSnapshot, id string) int {
	for i := range snapshot.ResolverSets {
		if snapshot.ResolverSets[i].ID == id {
			return i
		}
	}
	return -1
}

func findDNSRoute(routes []core.DNSRouteSnapshot, target core.DNSRouteSnapshot) int {
	for i, route := range routes {
		if route == target {
			return i
		}
	}
	return -1
}

func dnsListenLabel(listen string) string {
	if listen == "" {
		return "not configured"
	}
	return listen
}

func dnsEndpointLabel(endpoint string) string {
	if strings.Contains(endpoint, "@") {
		return "invalid endpoint (credentials hidden)"
	}
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.User != nil {
			return "invalid endpoint (credentials hidden)"
		}
	}
	return endpoint
}

func dnsRouteDetail(route core.DNSRouteSnapshot) string {
	matcher := ""
	switch {
	case route.Suffix != "":
		matcher = "suffix=" + route.Suffix
	case route.GeoSite != "":
		matcher = "geosite=" + route.GeoSite
	case route.Resource != "":
		matcher = "resource=" + route.Resource
	default:
		matcher = "matcher not configured"
	}
	return matcher + " -> resolver=" + route.ResolverSet
}

func validDNSListen(listen string) bool {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || strings.ContainsAny(listen, "\x00\r\n") {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && validDNSPort(port)
}

func validateDNSEndpoint(endpoint string, dnscrypt bool) error {
	if endpoint == "" || len(endpoint) > 256 || strings.TrimSpace(endpoint) != endpoint || strings.ContainsAny(endpoint, "\x00\r\n") {
		return fmt.Errorf("resolver endpoint is invalid")
	}
	host, scheme := "", "udp"
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Opaque != "" {
			return fmt.Errorf("resolver endpoint is invalid or contains credentials")
		}
		scheme = strings.ToLower(parsed.Scheme)
		switch scheme {
		case "udp", "tcp", "tls", "https", "quic":
		default:
			return fmt.Errorf("resolver endpoint scheme is unsupported")
		}
		host = parsed.Hostname()
		if host == "" || parsed.Port() != "" && !validDNSPort(parsed.Port()) || scheme != "https" && parsed.Path != "" && parsed.Path != "/" {
			return fmt.Errorf("resolver endpoint is invalid")
		}
	} else {
		var port string
		host, port, _ = net.SplitHostPort(endpoint)
		if host == "" || strings.Contains(host, "@") || !validDNSPort(port) {
			return fmt.Errorf("resolver endpoint is invalid or contains credentials")
		}
	}
	if dnscrypt && (scheme != "udp" && scheme != "tcp" || !dnsLoopbackHost(host)) {
		return fmt.Errorf("DNSCrypt endpoints must use a loopback UDP or TCP listener")
	}
	return nil
}

func validDNSPort(port string) bool {
	value, err := strconv.Atoi(port)
	return err == nil && value > 0 && value <= 65535
}

func dnsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validDNSDomain(value string) bool {
	value = strings.TrimSuffix(strings.TrimPrefix(value, "."), ".")
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	return true
}

func validDNSRuleToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
