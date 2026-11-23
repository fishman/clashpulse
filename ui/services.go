package ui

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
	"github.com/fishman/clashpulse/services"
)

var serviceIntervals = []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 6 * time.Hour}

const builtInDomains = "Built-in domains"

type serviceRow struct {
	value     *widget.Label
	help      *widget.RichText
	enabled   *widget.Check
	automatic *widget.Check
	check     *widget.Button
	edit      *widget.Button
}

// servicesSection is Settings > Services: one row per built-in service.
type servicesSection struct {
	send       sendIntent
	window     fyne.Window
	content    fyne.CanvasObject
	rows       map[string]*serviceRow
	snapshots  map[string]core.ServiceSnapshot
	resources  []string
	refreshing bool
}

func newServicesSection(send sendIntent, window fyne.Window) *servicesSection {
	s := &servicesSection{send: send, window: window, rows: make(map[string]*serviceRow), snapshots: make(map[string]core.ServiceSnapshot)}
	var rows []fyne.CanvasObject
	for _, def := range services.Definitions() {
		id := def.ID
		row := &serviceRow{value: widget.NewLabel(localize.T("Not checked")), help: newHelpText("")}
		row.enabled = widget.NewCheck(localize.T("Enable"), func(on bool) {
			s.sendEdit(id, func(edit *ipc.ServiceEdit) { edit.Enabled = on })
		})
		row.automatic = widget.NewCheck(localize.T("Automatic"), func(on bool) {
			s.sendEdit(id, func(edit *ipc.ServiceEdit) { edit.Automatic = on })
		})
		row.check = widget.NewButton(localize.T("Check now"), func() {
			s.send(ipc.Command{Kind: ipc.CommandCheckService, ServiceID: id})
		})
		row.edit = widget.NewButton(localize.T("Edit..."), func() { s.openEditor(id) })
		// Controls sit on their own line under the name so the pane fits a
		// compact window.
		controls := container.NewBorder(nil, nil, container.NewHBox(row.enabled, row.automatic), container.NewHBox(row.check, row.edit))
		below := []fyne.CanvasObject{controls, row.help}
		if id == "youtube" {
			below = append(below, newHelpText(localize.T("If YouTube's region notice lies beyond the read limit, the check relies on the country alone and may report usable.")))
		}
		rows = append(rows, settingRow(def.Name, row.value, nil, below...))
		s.rows[id] = row
		s.apply(id, core.ServiceSnapshot{ID: id, Name: def.Name})
	}
	s.content = settingsPane(settingsCard(localize.T("Services"), nil, cardBody(rows...)))
	return s
}

// update applies snapshots without sending: a missing entry shows the
// service as not configured.
func (s *servicesSection) update(items []core.ServiceSnapshot) {
	for _, def := range services.Definitions() {
		item := core.ServiceSnapshot{ID: def.ID, Name: def.Name}
		if index := slices.IndexFunc(items, func(item core.ServiceSnapshot) bool { return item.ID == def.ID }); index >= 0 {
			item = items[index]
		}
		if previous, ok := s.snapshots[def.ID]; ok && reflect.DeepEqual(previous, item) {
			continue
		}
		s.apply(def.ID, item)
	}
}

func intervalText(interval time.Duration) string {
	if interval%time.Hour == 0 {
		return fmt.Sprintf("%dh", interval/time.Hour)
	}
	return fmt.Sprintf("%dm", interval/time.Minute)
}

// setResources lists the enabled domain rule-set resources the editor offers.
func (s *servicesSection) setResources(items []core.ResourceSnapshot) {
	s.resources = s.resources[:0]
	for _, item := range items {
		if item.Enabled && item.RuleType == "domain" && (item.Kind == "rule-set" || item.Kind == "rule-provider") {
			s.resources = append(s.resources, item.ID)
		}
	}
}

func (s *servicesSection) apply(id string, item core.ServiceSnapshot) {
	s.snapshots[id] = item
	row := s.rows[id]
	s.refreshing = true
	row.enabled.SetChecked(item.Enabled)
	row.automatic.SetChecked(item.Automatic)
	s.refreshing = false
	if item.Enabled {
		row.automatic.Enable()
		row.check.Enable()
	} else {
		row.automatic.Disable()
		row.check.Disable()
	}
	row.value.SetText(serviceValue(item))
	timing := serviceTiming(item)
	setDetailText(row.help, timing)
	if timing == "" {
		row.help.Hide()
	} else {
		row.help.Show()
	}
}

func serviceValue(item core.ServiceSnapshot) string {
	if !item.Enabled {
		return localize.T("Off")
	}
	var outcome string
	switch item.Outcome {
	case "usable":
		outcome = localize.T("Usable")
	case "blocked":
		outcome = localize.T("Blocked")
	case "error":
		outcome = localize.T("Check failed")
	default:
		return localize.T("Not checked")
	}
	text := strings.TrimSpace(outcome + " " + item.Region)
	if item.Selected != "" {
		text += " - " + item.Selected
	}
	return text
}

func serviceTiming(item core.ServiceSnapshot) string {
	var parts []string
	if item.LastCheck > 0 {
		parts = append(parts, localize.T("Last check")+" "+time.Unix(item.LastCheck, 0).Format("15:04"))
	}
	if item.NextCheck > 0 {
		parts = append(parts, localize.T("next")+" "+time.Unix(item.NextCheck, 0).Format("15:04"))
	}
	if item.LastSwitch != "" {
		parts = append(parts, localize.Code("service.last-switch", item.LastSwitch))
	}
	return strings.Join(parts, " - ")
}

// sendEdit sends the row's current settings with one field changed.
func (s *servicesSection) sendEdit(id string, change func(*ipc.ServiceEdit)) {
	if s.refreshing {
		return
	}
	edit := ipc.NewServiceEdit(s.snapshots[id])
	change(edit)
	s.send(ipc.Command{Kind: ipc.CommandSetService, Service: edit})
}

func (s *servicesSection) openEditor(id string) {
	item := s.snapshots[id]
	regions := widget.NewEntry()
	regions.SetText(strings.Join(item.Regions, ", "))
	regions.SetPlaceHolder("US, JP")
	regions.Validator = func(text string) error {
		codes := ipc.ParseRegions(text)
		if len(codes) > services.MaxRegions || slices.ContainsFunc(codes, func(code string) bool { return !services.ValidRegion(code) }) {
			return fmt.Errorf(localize.T("Regions must be at most %d two-letter country codes."), services.MaxRegions)
		}
		return nil
	}
	// A hand-edited interval outside the presets stays selectable, so saving
	// another field never rewrites it.
	intervals := slices.Clone(serviceIntervals)
	current := time.Duration(item.IntervalSeconds) * time.Second
	if current == 0 {
		current = 30 * time.Minute
	}
	if !slices.Contains(intervals, current) {
		intervals = append(intervals, current)
		slices.Sort(intervals)
	}
	intervalLabels := make([]string, len(intervals))
	for i, interval := range intervals {
		intervalLabels[i] = intervalText(interval)
	}
	interval := widget.NewSelect(intervalLabels, nil)
	interval.SetSelectedIndex(slices.Index(intervals, current))
	failureLabels := make([]string, services.MaxFailures)
	for i := range failureLabels {
		failureLabels[i] = strconv.Itoa(i + 1)
	}
	failures := widget.NewSelect(failureLabels, nil)
	failures.SetSelectedIndex(max(0, min(services.MaxFailures, item.Failures)-1))
	// Snapshot updates rewrite s.resources while the dialog is open.
	resources := slices.Clone(s.resources)
	domains := widget.NewSelect(append([]string{localize.T(builtInDomains)}, resources...), nil)
	domains.SetSelectedIndex(0)
	if index := slices.Index(resources, item.DomainsResource); index >= 0 {
		domains.SetSelectedIndex(index + 1)
	}
	items := []*widget.FormItem{
		widget.NewFormItem(localize.T("Regions"), regions),
		widget.NewFormItem(localize.T("Check interval"), interval),
		widget.NewFormItem(localize.T("Failures before switching"), failures),
		widget.NewFormItem(localize.T("Domains"), domains),
	}
	form := dialog.NewForm(item.Name, localize.T("Save"), localize.T("Cancel"), items, func(confirmed bool) {
		if !confirmed {
			return
		}
		edit := ipc.NewServiceEdit(s.snapshots[id])
		edit.Regions = ipc.ParseRegions(regions.Text)
		edit.IntervalSeconds = uint32(intervals[max(0, interval.SelectedIndex())] / time.Second)
		edit.Failures = failures.SelectedIndex() + 1
		edit.DomainsResource = ""
		if index := domains.SelectedIndex(); index > 0 {
			edit.DomainsResource = resources[index-1]
		}
		s.send(ipc.Command{Kind: ipc.CommandSetService, Service: edit})
	}, s.window)
	showSized(form, s.window, formDialogWidth, false)
}
