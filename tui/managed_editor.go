package tui

import (
	"encoding/hex"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/notmutt/lib/tui/form"
)

func (m Model) openManagedModal(kind ModalKind, edit bool, targetID string) Model {
	var fields []form.Field
	if kind == ModalResource {
		resource := coreResource(m.snapshot.Snapshot.Resources, targetID)
		if edit && resource == nil {
			m.Notice = "Resource is no longer available."
			return m
		}
		id, resourceKind, format, ruleType, enabled := "", "", "", "", true
		if resource != nil {
			id, resourceKind, format, ruleType, enabled = resource.ID, resource.Kind, resource.Format, resource.RuleType, resource.Enabled
		}
		fields = []form.Field{
			{ID: "id", Label: "ID", Kind: form.Text, Value: id, ReadOnly: edit},
			{ID: "kind", Label: "Kind", Kind: form.Text, Value: resourceKind},
			{ID: "format", Label: "Format", Kind: form.Text, Value: format},
			{ID: "rule_type", Label: "Rule type", Kind: form.Text, Value: ruleType},
			{ID: "url", Label: "Source URL", Kind: form.Text, Sensitive: true},
			{ID: "enabled", Label: "Enabled", Kind: form.Toggle, Value: strconv.FormatBool(enabled)},
			{ID: "interval", Label: "Interval seconds", Kind: form.Text},
			{ID: "sha256", Label: "SHA-256 pin", Kind: form.Text},
		}
	} else if kind == ModalFilter {
		filter := coreFilter(m.snapshot.Snapshot.Filters, targetID)
		if edit && filter == nil {
			m.Notice = "Filter is no longer available."
			return m
		}
		id, resourceID, format, target, enabled := "", "", "", "", true
		if filter != nil {
			id, resourceID, format, target, enabled = filter.ID, filter.ResourceID, filter.Format, filter.Target, filter.Enabled
		}
		fields = []form.Field{
			{ID: "id", Label: "ID", Kind: form.Text, Value: id, ReadOnly: edit},
			{ID: "resource_id", Label: "Resource ID", Kind: form.Text, Value: resourceID},
			{ID: "format", Label: "Format", Kind: form.Text, Value: format},
			{ID: "target", Label: "Target", Kind: form.Text, Value: target},
			{ID: "enabled", Label: "Enabled", Kind: form.Toggle, Value: strconv.FormatBool(enabled)},
		}
	} else {
		return m
	}

	editor, err := form.New(fields)
	if err != nil {
		m.Notice = "Managed form unavailable."
		return m
	}
	if edit {
		editor.Move(1)
	}
	m.Modal = &Modal{Kind: kind, TargetID: targetID, Form: editor}
	m.Focus = FocusModal
	if edit {
		if kind == ModalResource {
			m.Notice = "Edit fields, then Ctrl+S to save; private URLs stay hidden. Use - to clear an optional rule type or SHA-256 pin."
		} else {
			m.Notice = "Edit fields, then Ctrl+S to save."
		}
	} else if kind == ModalResource {
		m.Notice = "Enter required fields, then Ctrl+S to save; private source stays hidden."
	} else {
		m.Notice = "Enter required fields, then Ctrl+S to save."
	}
	return m
}

func managedFormIntent(modal *Modal) (*ipc.Command, string) {
	if modal == nil || modal.Form == nil {
		return nil, "Managed form unavailable."
	}
	creating := modal.TargetID == ""
	if modal.Kind == ModalResource {
		id := modal.TargetID
		patch := &ipc.ResourceEdit{}
		for _, change := range modal.Form.Changes() {
			value := change.Value
			if len(value) > 4096 {
				return nil, "Field is too long."
			}
			switch change.ID {
			case "id":
				if creating {
					id = value
					if !validSubscriptionID(value) {
						return nil, "Enter a valid stable ID."
					}
				}
			case "kind":
				if !validResourceKind(value) {
					return nil, "Choose a supported resource kind."
				}
				patch.Kind = &value
			case "format":
				if !validResourceFormat(value) {
					return nil, "Choose a supported resource format."
				}
				patch.Format = &value
			case "rule_type":
				if value == "-" {
					value = ""
				} else if value != "" && !validResourceRuleType(value) {
					return nil, "Choose domain, ipcidr, or classical."
				}
				patch.RuleType = &value
			case "url":
				if strings.ContainsAny(value, "\x00\r\n") || value != "" && !validManagedResourceSource(value) {
					return nil, "Source must be HTTPS without credentials or an absolute local path."
				}
				if value != "" {
					patch.URL = &value
				}
			case "enabled":
				enabled, err := strconv.ParseBool(value)
				if err != nil {
					return nil, "Enabled must be true or false."
				}
				patch.Enabled = &enabled
			case "interval":
				if len(value) > 10 || !validSubscriptionNumber(value, 1, uint64(^uint32(0))) {
					return nil, "Interval must be between 1 and 4294967295 seconds."
				}
				interval, _ := strconv.ParseUint(value, 10, 32)
				seconds := uint32(interval)
				patch.IntervalSeconds = &seconds
			case "sha256":
				if value == "-" {
					value = ""
				} else if value != "" {
					if len(value) != 64 {
						return nil, "SHA-256 must be 64 hexadecimal characters."
					}
					if _, err := hex.DecodeString(value); err != nil {
						return nil, "SHA-256 must be hexadecimal."
					}
				}
				patch.SHA256 = &value
			default:
				return nil, "Managed form unavailable."
			}
		}
		if creating {
			if !validSubscriptionID(id) {
				return nil, "Enter a valid stable ID."
			}
			if patch.Kind == nil || patch.Format == nil || patch.URL == nil || patch.IntervalSeconds == nil {
				return nil, "Kind, format, source URL, and positive interval are required."
			}
			ruleType := ""
			if patch.RuleType != nil {
				ruleType = *patch.RuleType
			}
			if !validResourceDeclaration(*patch.Kind, *patch.Format, ruleType) {
				return nil, "Resource kind, format, and rule type are incompatible."
			}
			if patch.Enabled == nil {
				enabled := true
				patch.Enabled = &enabled
			}
		}
		if patch.Kind == nil && patch.Format == nil && patch.RuleType == nil && patch.URL == nil && patch.Enabled == nil && patch.IntervalSeconds == nil && patch.SHA256 == nil {
			return nil, ""
		}
		return &ipc.Command{Kind: ipc.CommandPutResource, ResourceID: id, Resource: patch}, ""
	}
	if modal.Kind != ModalFilter {
		return nil, "Managed form unavailable."
	}
	id := modal.TargetID
	patch := &ipc.FilterEdit{}
	for _, change := range modal.Form.Changes() {
		value := change.Value
		if len(value) > 4096 {
			return nil, "Field is too long."
		}
		switch change.ID {
		case "id":
			if creating {
				id = value
				if !validSubscriptionID(value) {
					return nil, "Enter a valid stable ID."
				}
			}
		case "resource_id":
			if !validSubscriptionID(value) {
				return nil, "Enter a valid resource ID."
			}
			patch.ResourceID = &value
		case "format":
			if !validFilterFormat(value) {
				return nil, "Filter format must be yaml, text, or mrs."
			}
			patch.Format = &value
		case "target":
			if len(value) > 128 || value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, ",\r\n\t\x00") {
				return nil, "Target is required and cannot contain commas or control characters."
			}
			patch.Target = &value
		case "enabled":
			enabled, err := strconv.ParseBool(value)
			if err != nil {
				return nil, "Enabled must be true or false."
			}
			patch.Enabled = &enabled
		default:
			return nil, "Managed form unavailable."
		}
	}
	if creating {
		if !validSubscriptionID(id) {
			return nil, "Enter a valid stable ID."
		}
		if patch.ResourceID == nil || patch.Format == nil || patch.Target == nil {
			return nil, "Resource ID, format, and target are required."
		}
		if patch.Enabled == nil {
			enabled := true
			patch.Enabled = &enabled
		}
	}
	if patch.ResourceID == nil && patch.Format == nil && patch.Target == nil && patch.Enabled == nil {
		return nil, ""
	}
	return &ipc.Command{Kind: ipc.CommandPutFilter, FilterID: id, Filter: patch}, ""
}

func validManagedResourceSource(raw string) bool {
	if filepath.IsAbs(raw) {
		return filepath.Separator != '\\' || !strings.HasPrefix(raw, `\\`)
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Opaque == ""
}

func validResourceKind(value string) bool {
	switch value {
	case "geoip.dat", "geosite.dat", "Country.mmdb", "rule-set", "rule-provider":
		return true
	default:
		return false
	}
}

func validResourceFormat(value string) bool {
	switch value {
	case "dat", "mmdb", "yaml", "text", "mrs":
		return true
	default:
		return false
	}
}

func validFilterFormat(value string) bool {
	switch value {
	case "yaml", "text", "mrs":
		return true
	default:
		return false
	}
}

func validResourceRuleType(value string) bool {
	switch value {
	case "domain", "ipcidr", "classical":
		return true
	default:
		return false
	}
}

func validResourceDeclaration(kind, format, ruleType string) bool {
	switch kind {
	case "geoip.dat", "geosite.dat":
		return format == "dat" && ruleType == ""
	case "Country.mmdb":
		return format == "mmdb" && ruleType == ""
	case "rule-set", "rule-provider":
		return validFilterFormat(format) && validResourceRuleType(ruleType) && !(format == "mrs" && ruleType == "classical")
	default:
		return false
	}
}

func coreResource(resources []core.ResourceSnapshot, id string) *core.ResourceSnapshot {
	for i := range resources {
		if resources[i].ID == id {
			return &resources[i]
		}
	}
	return nil
}

func coreFilter(filters []core.FilterSnapshot, id string) *core.FilterSnapshot {
	for i := range filters {
		if filters[i].ID == id {
			return &filters[i]
		}
	}
	return nil
}
