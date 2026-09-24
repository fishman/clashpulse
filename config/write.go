package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
)

func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	success = true
	return nil
}

type SettingsPatch struct {
	Binary                       *string
	SystemProxyEnabled           *bool
	DNSListen                    *string
	MonitorEnabled               *bool
	MonitorTestURL               *string
	MonitorInterval              *time.Duration
	MonitorTimeout               *time.Duration
	MonitorConcurrency           *int
	MonitorThreshold             *time.Duration
	AlertThreshold               *time.Duration
	MonitorConsecutiveBadSamples *int
	MonitorMinImprovement        *time.Duration
	MonitorCooldown              *time.Duration
	MonitorJitter                *time.Duration
}

// PatchSettings edits only the explicit user intent fields in config.toml.
func PatchSettings(path string, current Snapshot, patch SettingsPatch) error {
	var document configDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	next := cloneSnapshot(current)
	if patch.Binary != nil {
		document.Mihomo.Binary = *patch.Binary
		next.Mihomo.Binary = *patch.Binary
	}
	if patch.SystemProxyEnabled != nil {
		document.SystemProxy.Enabled = *patch.SystemProxyEnabled
		next.App.SystemProxy.Enabled = *patch.SystemProxyEnabled
	}
	if patch.DNSListen != nil {
		document.DNS.Listen = *patch.DNSListen
		next.DNS.Listen = *patch.DNSListen
	}
	if patch.MonitorEnabled != nil {
		document.Monitor.Enabled = patch.MonitorEnabled
		next.Monitor.Enabled = *patch.MonitorEnabled
	}
	if patch.MonitorInterval != nil {
		value := patch.MonitorInterval.String()
		document.Monitor.Interval = &value
		next.Monitor.Interval = *patch.MonitorInterval
	}
	if patch.MonitorTestURL != nil {
		document.Monitor.TestURL = patch.MonitorTestURL
		next.Monitor.TestURL = *patch.MonitorTestURL
	}
	if patch.MonitorTimeout != nil {
		value := patch.MonitorTimeout.String()
		document.Monitor.Timeout = &value
		next.Monitor.Timeout = *patch.MonitorTimeout
	}
	if patch.MonitorConcurrency != nil {
		document.Monitor.Concurrency = patch.MonitorConcurrency
		next.Monitor.Concurrency = *patch.MonitorConcurrency
	}
	if patch.MonitorThreshold != nil {
		value := patch.MonitorThreshold.String()
		document.Monitor.Threshold = &value
		next.Monitor.Threshold = *patch.MonitorThreshold
	}
	if patch.MonitorConsecutiveBadSamples != nil {
		document.Monitor.ConsecutiveBadSamples = patch.MonitorConsecutiveBadSamples
		next.Monitor.ConsecutiveBadSamples = *patch.MonitorConsecutiveBadSamples
	}
	if patch.MonitorMinImprovement != nil {
		value := patch.MonitorMinImprovement.String()
		document.Monitor.MinImprovement = &value
		next.Monitor.MinImprovement = *patch.MonitorMinImprovement
	}
	if patch.MonitorCooldown != nil {
		value := patch.MonitorCooldown.String()
		document.Monitor.Cooldown = &value
		next.Monitor.Cooldown = *patch.MonitorCooldown
	}
	if patch.MonitorJitter != nil {
		value := patch.MonitorJitter.String()
		document.Monitor.Jitter = &value
		next.Monitor.Jitter = *patch.MonitorJitter
	}
	if patch.AlertThreshold != nil {
		value := patch.AlertThreshold.String()
		document.Monitor.AlertThreshold = &value
		next.Monitor.AlertThreshold = *patch.AlertThreshold
	}
	if reflect.DeepEqual(next, current) {
		return nil
	}
	if err := validateSnapshot(next); err != nil {
		return err
	}
	return writeConfigDocument(path, document)
}

// PatchAutomation persists explicit opt-in for a stable opaque group identity.
func PatchAutomation(path string, current Snapshot, groupID string, enabled bool) error {
	var document configDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	groups := append([]string(nil), document.Monitor.AutomatedGroups...)
	if enabled {
		if !slices.Contains(groups, groupID) {
			groups = append(groups, groupID)
		}
	} else {
		groups = slices.DeleteFunc(groups, func(id string) bool { return id == groupID })
	}
	sort.Strings(groups)
	next := cloneSnapshot(current)
	next.Monitor.AutomatedGroups = groups
	if reflect.DeepEqual(next, current) {
		return nil
	}
	if err := validateSnapshot(next); err != nil {
		return err
	}
	document.Monitor.AutomatedGroups = groups
	return writeConfigDocument(path, document)
}

func writeConfigDocument(path string, document configDoc) error {
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(document); err != nil {
		return err
	}
	return Write(path, encoded.Bytes())
}
