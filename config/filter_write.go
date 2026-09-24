package config

import (
	"bytes"
	"os"
	"reflect"
	"slices"

	"github.com/BurntSushi/toml"
)

type FilterEdit struct {
	Resource *string
	Format   *ResourceFormat
	Target   *string
	Enabled  *bool
}

// PatchFilter updates supplied fields for a stable filter ID, creating it when absent.
func PatchFilter(path string, current Snapshot, id string, patch FilterEdit) error {
	var document filtersDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	next := cloneSnapshot(current)
	docIndex := -1
	for i := range document.Filters {
		if document.Filters[i].ID == id {
			docIndex = i
			break
		}
	}
	if docIndex < 0 {
		document.Filters = append(document.Filters, filterDoc{ID: id})
		docIndex = len(document.Filters) - 1
	}
	item := &document.Filters[docIndex]
	if patch.Resource != nil {
		item.Resource = *patch.Resource
	}
	if patch.Format != nil {
		item.Format = string(*patch.Format)
	}
	if patch.Target != nil {
		item.Target = *patch.Target
	}
	if patch.Enabled != nil {
		item.Enabled = *patch.Enabled
	}
	syncFilterSnapshot(&next, document)
	applyDefaults(&next)
	if err := validateSnapshot(next); err != nil {
		return err
	}
	if reflect.DeepEqual(next, current) {
		return nil
	}
	return writeFilters(path, document)
}

func DeleteFilter(path string, current Snapshot, id string) error {
	var document filtersDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	found := false
	document.Filters = slices.DeleteFunc(document.Filters, func(item filterDoc) bool {
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
	syncFilterSnapshot(&next, document)
	if err := validateSnapshot(next); err != nil {
		return err
	}
	return writeFilters(path, document)
}

func syncFilterSnapshot(snapshot *Snapshot, document filtersDoc) {
	snapshot.Filters = snapshot.Filters[:0]
	for _, item := range document.Filters {
		snapshot.Filters = append(snapshot.Filters, Filter{
			ID: item.ID, Resource: item.Resource, Format: ResourceFormat(item.Format), Target: item.Target, Enabled: item.Enabled,
		})
	}
}

func writeFilters(path string, document filtersDoc) error {
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(document); err != nil {
		return err
	}
	return Write(path, encoded.Bytes())
}
