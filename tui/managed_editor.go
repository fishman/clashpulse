package tui

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fishman/clashpulse/ipc"
)

const (
	resourceFieldID = iota
	resourceFieldKind
	resourceFieldFormat
	resourceFieldRuleType
	resourceFieldURL
	resourceFieldEnabled
	resourceFieldInterval
	resourceFieldSHA256
	resourceFieldCount
)

const (
	filterFieldID = iota
	filterFieldResourceID
	filterFieldFormat
	filterFieldTarget
	filterFieldEnabled
	filterFieldCount
)

func (m Model) openManagedModal(kind ModalKind, edit bool, targetID string) Model {
	form := managedForm{edit: edit, targetID: targetID}
	if edit {
		form.step = 1
	}
	m.Modal = &Modal{Kind: kind, TargetID: targetID, Input: form.values[form.step], managed: form}
	m.Focus = FocusModal
	if edit {
		m.Notice = "Blank fields keep current values; - clears an optional resource rule type or SHA-256."
	} else {
		m.Notice = "Enter a stable ID and required fields; enabled defaults to yes."
	}
	return m
}

func (m Model) handleManagedKey(key string) (Model, *ipc.Command, bool) {
	modal := *m.Modal
	form := modal.managed
	switch normalizeKey(key) {
	case "backspace":
		input := []rune(modal.Input)
		if len(input) > 0 {
			modal.Input = string(input[:len(input)-1])
			m.Modal = &modal
		}
		return m, nil, false
	case "shift+tab":
		firstEditable := 0
		if form.edit {
			firstEditable = 1
		}
		if form.step > firstEditable {
			form.values[form.step] = strings.TrimSpace(modal.Input)
			form.step--
			modal.managed = form
			modal.Input = form.values[form.step]
			m.Modal = &modal
			m.Notice = ""
		}
		return m, nil, false
	case "enter":
		form.values[form.step] = strings.TrimSpace(modal.Input)
		if form.step == 0 && !form.edit {
			form.targetID = form.values[0]
			modal.TargetID = form.targetID
		}
		if notice := validateManagedField(modal.Kind, form, form.step); notice != "" {
			m.Notice = notice
			modal.managed = form
			m.Modal = &modal
			return m, nil, false
		}
		if form.step+1 < managedFieldCount(modal.Kind) {
			form.step++
			modal.managed = form
			modal.Input = form.values[form.step]
			m.Modal = &modal
			m.Notice = ""
			return m, nil, false
		}
		command, notice := managedIntent(modal.Kind, form)
		if notice != "" {
			m.Notice = notice
			modal.managed = form
			m.Modal = &modal
			return m, nil, false
		}
		m.Modal = nil
		m.Focus = FocusContent
		m.Notice = ""
		return m, command, false
	default:
		if utf8.ValidString(key) && utf8.RuneCountInString(key) == 1 && unicode.IsPrint([]rune(key)[0]) && len(modal.Input)+len(key) <= managedFieldMaxBytes(modal.Kind, form.step) {
			modal.Input += key
			m.Modal = &modal
		}
		return m, nil, false
	}
}

func managedModalPrompt(modal *Modal) string {
	form := modal.managed
	count := managedFieldCount(modal.Kind)
	current, total, action := form.step+1, count, "New"
	if form.edit {
		current, total, action = form.step, count-1, "Edit"
	}
	name := ""
	if modal.Kind == ModalResource {
		name = "resource"
		switch form.step {
		case resourceFieldID:
			name += " ID (required)"
		case resourceFieldKind:
			name += " kind (blank keeps)"
		case resourceFieldFormat:
			name += " format (blank keeps)"
		case resourceFieldRuleType:
			name += " rule type (blank keeps; - clears)"
		case resourceFieldURL:
			if form.edit {
				name += " source URL (blank keeps private URL)"
			} else {
				name += " source URL (required)"
			}
		case resourceFieldEnabled:
			name += " enabled yes/no (blank keeps; new defaults yes)"
		case resourceFieldInterval:
			name += " interval seconds (positive; blank keeps)"
		case resourceFieldSHA256:
			name += " SHA-256 (optional; - clears)"
		}
	} else {
		name = "filter"
		switch form.step {
		case filterFieldID:
			name += " ID (required)"
		case filterFieldResourceID:
			name += " resource ID (blank keeps)"
		case filterFieldFormat:
			name += " format (blank keeps)"
		case filterFieldTarget:
			name += " target (blank keeps)"
		case filterFieldEnabled:
			name += " enabled yes/no (blank keeps; new defaults yes)"
		}
	}
	return fmt.Sprintf("%s %s %d/%d - %s", action, modal.Kind, current, total, name)
}

func managedFieldCount(kind ModalKind) int {
	if kind == ModalResource {
		return resourceFieldCount
	}
	return filterFieldCount
}

func managedFieldMaxBytes(kind ModalKind, field int) int {
	if kind == ModalResource {
		switch field {
		case resourceFieldURL:
			return 4096
		case resourceFieldInterval:
			return 10
		default:
			return 64
		}
	}
	if kind == ModalFilter && field == filterFieldTarget {
		return 128
	}
	return 64
}

func validateManagedField(kind ModalKind, form managedForm, field int) string {
	value := form.values[field]
	if len(value) > managedFieldMaxBytes(kind, field) {
		return "Field is too long."
	}
	if kind == ModalResource {
		switch field {
		case resourceFieldID:
			if !validSubscriptionID(value) {
				return "ID must start with a letter or digit and contain only letters, digits, dot, underscore, or hyphen (max 64)."
			}
		case resourceFieldKind:
			if !form.edit && value == "" {
				return "Resource kind is required."
			}
		case resourceFieldFormat:
			if !form.edit && value == "" {
				return "Resource format is required."
			}
		case resourceFieldRuleType:
			resourceKind := form.values[resourceFieldKind]
			if !form.edit && (resourceKind == "rule-set" || resourceKind == "rule-provider") && (value == "" || value == "-") {
				return "Rule type is required for rule-set resources."
			}
		case resourceFieldURL:
			if !form.edit && value == "" {
				return "A source URL is required for a new resource."
			}
			if strings.ContainsAny(value, "\x00\r\n") {
				return "Source URL cannot contain control characters."
			}
			if value != "" && !validManagedResourceSource(value) {
				return "Source must be HTTPS without credentials or an absolute local path."
			}
		case resourceFieldEnabled:
			if value != "" && !validYesNo(value) {
				return "Enter yes or no, or leave blank to keep the current value."
			}
		case resourceFieldInterval:
			if value == "" && !form.edit {
				return "A positive update interval is required for a new resource."
			}
			if value != "" && !validSubscriptionNumber(value, 1, uint64(^uint32(0))) {
				return "Interval must be between 1 and 4294967295 seconds."
			}
		case resourceFieldSHA256:
			if value != "" && value != "-" {
				if len(value) != 64 {
					return "SHA-256 must be 64 hexadecimal characters."
				}
				if _, err := hex.DecodeString(value); err != nil {
					return "SHA-256 must be hexadecimal."
				}
			}
		}
		return ""
	}
	switch field {
	case filterFieldID:
		if !validSubscriptionID(value) {
			return "ID must start with a letter or digit and contain only letters, digits, dot, underscore, or hyphen (max 64)."
		}
	case filterFieldResourceID:
		if value != "" && !validSubscriptionID(value) {
			return "Resource ID must start with a letter or digit and contain only letters, digits, dot, underscore, or hyphen (max 64)."
		}
		if !form.edit && value == "" {
			return "A resource ID is required."
		}
	case filterFieldFormat:
		if !form.edit && value == "" {
			return "Filter format is required."
		}
	case filterFieldTarget:
		if !form.edit && value == "" {
			return "Filter target is required."
		}
	case filterFieldEnabled:
		if value != "" && !validYesNo(value) {
			return "Enter yes or no, or leave blank to keep the current value."
		}
	}
	return ""
}

func validManagedResourceSource(raw string) bool {
	if filepath.IsAbs(raw) {
		return filepath.Separator != '\\' || !strings.HasPrefix(raw, `\\`)
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Opaque == ""
}

func managedIntent(kind ModalKind, form managedForm) (*ipc.Command, string) {
	if kind == ModalResource {
		patch := &ipc.ResourceEdit{}
		if value := form.values[resourceFieldKind]; value != "" {
			patch.Kind = &value
		}
		if value := form.values[resourceFieldFormat]; value != "" {
			patch.Format = &value
		}
		ruleType := form.values[resourceFieldRuleType]
		if ruleType == "-" {
			ruleType = ""
			patch.RuleType = &ruleType
		} else if ruleType != "" {
			patch.RuleType = &ruleType
		}
		if value := form.values[resourceFieldURL]; value != "" {
			patch.URL = &value
		}
		patch.Enabled = managedEnabled(form.values[resourceFieldEnabled])
		if patch.Enabled == nil && !form.edit {
			enabled := true
			patch.Enabled = &enabled
		}
		if value := form.values[resourceFieldInterval]; value != "" {
			parsed, _ := strconv.ParseUint(value, 10, 32)
			interval := uint32(parsed)
			patch.IntervalSeconds = &interval
		}
		sha := form.values[resourceFieldSHA256]
		if sha == "-" {
			sha = ""
			patch.SHA256 = &sha
		} else if sha != "" {
			patch.SHA256 = &sha
		}
		if patch.Kind == nil && patch.Format == nil && patch.RuleType == nil && patch.URL == nil && patch.Enabled == nil && patch.IntervalSeconds == nil && patch.SHA256 == nil {
			return nil, "Enter at least one value to update."
		}
		return &ipc.Command{Kind: ipc.CommandPutResource, ResourceID: form.targetID, Resource: patch}, ""
	}
	patch := &ipc.FilterEdit{}
	if value := form.values[filterFieldResourceID]; value != "" {
		patch.ResourceID = &value
	}
	if value := form.values[filterFieldFormat]; value != "" {
		patch.Format = &value
	}
	if value := form.values[filterFieldTarget]; value != "" {
		patch.Target = &value
	}
	patch.Enabled = managedEnabled(form.values[filterFieldEnabled])
	if patch.Enabled == nil && !form.edit {
		enabled := true
		patch.Enabled = &enabled
	}
	if patch.ResourceID == nil && patch.Format == nil && patch.Target == nil && patch.Enabled == nil {
		return nil, "Enter at least one value to update."
	}
	return &ipc.Command{Kind: ipc.CommandPutFilter, FilterID: form.targetID, Filter: patch}, ""
}

func managedEnabled(value string) *bool {
	if value == "" {
		return nil
	}
	parsed := strings.EqualFold(value, "yes") || strings.EqualFold(value, "y") || strings.EqualFold(value, "true")
	return &parsed
}
