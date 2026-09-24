package config

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
)

type ResourceEdit struct {
	Kind     *ResourceKind
	Format   *ResourceFormat
	RuleType *RuleType
	URL      *string
	Enabled  *bool
	Interval *time.Duration
	SHA256   *string
}

// PatchResource changes only supplied user-intent fields. An absent URL
// retains the private source; adding a resource requires one.
func PatchResource(path string, current Snapshot, id string, patch ResourceEdit) error {
	var document resourcesDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}

	next := cloneSnapshot(current)
	index := -1
	for i := range document.Resources {
		if document.Resources[i].ID == id {
			index = i
			break
		}
	}
	stateIndex := -1
	if index < 0 {
		if patch.URL == nil {
			return errors.New("resources.toml: resource.url: required for new resource")
		}
		document.Resources = append(document.Resources, resourceDoc{ID: id})
		next.Resources = append(next.Resources, Resource{ID: id, Interval: 12 * time.Hour})
		index = len(document.Resources) - 1
		stateIndex = len(next.Resources) - 1
	} else {
		for i := range next.Resources {
			if next.Resources[i].ID == id {
				stateIndex = i
				break
			}
		}
		if stateIndex < 0 {
			return errors.New("resources.toml: resource.id: missing from current snapshot")
		}
	}

	item, state := &document.Resources[index], &next.Resources[stateIndex]
	if patch.Kind != nil {
		item.Kind, state.Kind = string(*patch.Kind), *patch.Kind
	}
	if patch.Format != nil {
		item.Format, state.Format = string(*patch.Format), *patch.Format
	}
	if patch.RuleType != nil {
		item.RuleType, state.RuleType = string(*patch.RuleType), *patch.RuleType
	}
	if patch.URL != nil {
		item.URL, state.URL = *patch.URL, *patch.URL
	}
	if patch.Enabled != nil {
		item.Enabled, state.Enabled = *patch.Enabled, *patch.Enabled
	}
	if patch.Interval != nil {
		value := patch.Interval.String()
		item.Interval, state.Interval = &value, *patch.Interval
	}
	if patch.SHA256 != nil {
		item.SHA256, state.SHA256 = *patch.SHA256, *patch.SHA256
	}
	if err := validateSnapshot(next); err != nil {
		return err
	}
	if reflect.DeepEqual(next, current) {
		return nil
	}
	return writeResources(path, document)
}

func DeleteResource(path string, current Snapshot, id string) error {
	var document resourcesDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	found := false
	document.Resources = slices.DeleteFunc(document.Resources, func(item resourceDoc) bool {
		if item.ID == id {
			found = true
			return true
		}
		return false
	})
	if !found {
		return os.ErrNotExist
	}
	next := cloneSnapshot(current)
	next.Resources = slices.DeleteFunc(next.Resources, func(item Resource) bool { return item.ID == id })
	if err := validateSnapshot(next); err != nil {
		return err
	}
	return writeResources(path, document)
}

func writeResources(path string, document resourcesDoc) error {
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(document); err != nil {
		return err
	}
	return Write(path, encoded.Bytes())
}
