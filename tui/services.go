package tui

import (
	"strconv"
	"strings"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
	"github.com/fishman/clashpulse/services"
	"github.com/fishman/notmutt/lib/tui/form"
)

// serviceRows lists every built-in service; one the snapshot has not
// published yet shows as not checked and accepts no action.
func serviceRows(published []core.ServiceSnapshot) []Row {
	definitions := services.Definitions()
	rows := make([]Row, 0, len(definitions))
	for _, def := range definitions {
		value, detail := localize.T("Not checked"), ""
		if item, ok := findService(published, def.ID); ok {
			value, detail = serviceValue(item), serviceTiming(item)
		}
		rows = append(rows, Row{ID: "service:" + def.ID, Title: def.Name, Detail: detail,
			Cells: []string{def.Name, value, ""}, kind: rowService, serviceID: def.ID, action: "service_toggle"})
	}
	return rows
}

func findService(published []core.ServiceSnapshot, id string) (core.ServiceSnapshot, bool) {
	for _, item := range published {
		if item.ID == id {
			return item, true
		}
	}
	return core.ServiceSnapshot{}, false
}

func serviceValue(item core.ServiceSnapshot) string {
	if !item.Enabled {
		return localize.T("Off")
	}
	var text string
	switch item.Outcome {
	case "usable":
		text = localize.T("Usable")
	case "blocked":
		text = localize.T("Blocked")
	case "error":
		text = localize.T("Check failed")
	default:
		text = localize.T("Not checked")
	}
	text = strings.TrimSpace(text + " " + item.Region)
	if item.Automatic {
		text += "; " + localize.T("Automatic")
	}
	if item.Selected != "" {
		text += "; " + item.Selected
	}
	return text
}

func serviceTiming(item core.ServiceSnapshot) string {
	var parts []string
	if item.LastCheck > 0 {
		parts = append(parts, localize.T("Last check")+" "+timeLabel(item.LastCheck))
	}
	if item.NextCheck > 0 {
		parts = append(parts, localize.T("next")+" "+timeLabel(item.NextCheck))
	}
	if item.LastSwitch != "" {
		parts = append(parts, localize.Code("service.last-switch", item.LastSwitch))
	}
	return strings.Join(parts, "; ")
}

// selectedService returns the published settings of the selected service row.
func (m Model) selectedService() (core.ServiceSnapshot, bool) {
	row, ok := m.selectedRow()
	if !ok || row.kind != rowService {
		return core.ServiceSnapshot{}, false
	}
	return findService(m.snapshot.Snapshot.Services, row.serviceID)
}

func (m Model) serviceAction(action string) (Model, *ipc.Command, bool) {
	item, ok := m.selectedService()
	if !ok {
		m.Notice = localize.T("Select a service whose settings the application has published.")
		return m, nil, false
	}
	if action == "service_check" {
		if !item.Enabled {
			m.Notice = localize.T("Enable the service check before checking it.")
			return m, nil, false
		}
		return m, &ipc.Command{Kind: ipc.CommandCheckService, ServiceID: item.ID}, false
	}
	edit := ipc.NewServiceEdit(item)
	if action == "service_toggle" {
		edit.Enabled = !edit.Enabled
	} else {
		edit.Automatic = !edit.Automatic
	}
	return m, &ipc.Command{Kind: ipc.CommandSetService, Service: edit}, false
}

func (m Model) openServiceModal(id string) Model {
	item, ok := findService(m.snapshot.Snapshot.Services, id)
	if !ok {
		m.Notice = localize.T("Select a service whose settings the application has published.")
		return m
	}
	editor, err := form.New([]form.Field{
		{ID: "regions", Label: localize.T("Regions"), Kind: form.Text, Value: strings.Join(item.Regions, ", ")},
		{ID: "interval", Label: localize.T("Interval seconds"), Kind: form.Text, Value: strconv.FormatUint(uint64(item.IntervalSeconds), 10)},
		{ID: "failures", Label: localize.T("Switch after failures"), Kind: form.Text, Value: strconv.Itoa(item.Failures)},
		{ID: "domains", Label: localize.T("Domains resource ID"), Kind: form.Text, Value: item.DomainsResource},
	})
	if err != nil {
		m.Notice = localize.T("Service settings form unavailable.")
		return m
	}
	m.Modal = &Modal{Kind: ModalService, TargetID: id, Form: editor}
	m.Focus = FocusModal
	m.Notice = localize.T("Regions: comma-separated country codes; an empty domains resource uses the built-in domains.")
	return m
}

func (m Model) serviceFormIntent(modal *Modal) (*ipc.Command, string) {
	item, ok := findService(m.snapshot.Snapshot.Services, modal.TargetID)
	if !ok {
		return nil, localize.T("Select a service whose settings the application has published.")
	}
	edit := ipc.NewServiceEdit(item)
	for _, change := range modal.Form.Changes() {
		switch change.ID {
		case "regions":
			edit.Regions = ipc.ParseRegions(change.Value)
			for _, region := range edit.Regions {
				if len(region) != 2 || strings.Trim(region, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
					return nil, localize.T("Regions must be two-letter country codes.")
				}
			}
		case "interval":
			value, err := strconv.ParseUint(change.Value, 10, 32)
			if err != nil || value < 300 || value > 30*86400 {
				return nil, localize.T("Interval must be 300 to 2592000 seconds.")
			}
			edit.IntervalSeconds = uint32(value)
		case "failures":
			value, err := strconv.Atoi(change.Value)
			if err != nil || value < 1 || value > 10 {
				return nil, localize.T("Failures before switching must be 1 to 10.")
			}
			edit.Failures = value
		case "domains":
			edit.DomainsResource = strings.TrimSpace(change.Value)
		}
	}
	return &ipc.Command{Kind: ipc.CommandSetService, Service: edit}, ""
}
