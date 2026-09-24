package sysproxy

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type networkProxySettings struct {
	enabled bool
	host    string
	port    int
	auth    bool
}

func parseDefaultInterface(output string) (string, error) {
	var iface string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "interface:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
		if value == "" || iface != "" {
			return "", errors.New("route output has a missing or duplicate default interface")
		}
		iface = value
	}
	if iface == "" {
		return "", errors.New("route output does not identify a default interface")
	}
	return iface, nil
}

func parseNetworkService(output, device string) (string, error) {
	var service string
	var candidate string
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "(") && strings.Contains(line, ") ") {
			candidate = ""
			nameStart := strings.Index(line, ") ") + 2
			name := strings.TrimSpace(line[nameStart:])
			disabled := strings.HasPrefix(name, "*")
			name = strings.TrimSpace(strings.TrimPrefix(name, "*"))
			if !disabled && name != "" {
				candidate = name
			}
			continue
		}
		if candidate == "" || !strings.HasPrefix(line, "(Hardware Port:") {
			continue
		}
		marker := strings.LastIndex(line, ", Device: ")
		if marker < 0 || !strings.HasSuffix(line, ")") {
			continue
		}
		hardwareDevice := strings.TrimSpace(strings.TrimSuffix(line[marker+len(", Device: "):], ")"))
		if hardwareDevice != device {
			continue
		}
		if service != "" {
			return "", fmt.Errorf("network service list maps device %q to multiple enabled services", device)
		}
		service = candidate
	}
	if service == "" {
		return "", fmt.Errorf("no enabled network service maps to default device %q", device)
	}
	return service, nil
}

func parseNetworkProxy(output string) (networkProxySettings, error) {
	fields := make(map[string]string, 4)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok {
			fields[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	for _, key := range []string{"enabled", "server", "port", "authenticated proxy enabled"} {
		if _, ok := fields[key]; !ok {
			return networkProxySettings{}, fmt.Errorf("networksetup proxy output is missing %q", key)
		}
	}
	enabled, err := parseNetworkBool(fields["enabled"])
	if err != nil {
		return networkProxySettings{}, fmt.Errorf("networksetup proxy enabled state: %w", err)
	}
	auth, err := parseNetworkBool(fields["authenticated proxy enabled"])
	if err != nil {
		return networkProxySettings{}, fmt.Errorf("networksetup authenticated proxy state: %w", err)
	}
	host := fields["server"]
	if host == "" {
		return networkProxySettings{}, errors.New("networksetup proxy output has an empty server")
	}
	port, err := strconv.Atoi(fields["port"])
	if err != nil || port < 1 || port > 65535 {
		return networkProxySettings{}, errors.New("networksetup proxy output has an invalid port")
	}
	return networkProxySettings{enabled: enabled, host: host, port: port, auth: auth}, nil
}

func parsePACEnabled(output string) (bool, error) {
	return parseNamedNetworkBool(output, "enabled")
}

func parseAutoDiscoveryEnabled(output string) (bool, error) {
	return parseNamedNetworkBool(output, "auto proxy discovery", "proxy auto discovery", "proxy auto discover")
}

func parseNamedNetworkBool(output string, expectedKeys ...string) (bool, error) {
	var result *bool
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		matched := false
		for _, expectedKey := range expectedKeys {
			if strings.EqualFold(strings.TrimSpace(key), expectedKey) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if result != nil {
			return false, fmt.Errorf("networksetup output has duplicate %q state", strings.Join(expectedKeys, ", "))
		}
		parsed, err := parseNetworkBool(strings.TrimSpace(value))
		if err != nil {
			return false, fmt.Errorf("networksetup %s state: %w", strings.Join(expectedKeys, ", "), err)
		}
		result = &parsed
	}
	if result == nil {
		return false, fmt.Errorf("networksetup output is missing %q state", strings.Join(expectedKeys, ", "))
	}
	return *result, nil
}

func parseNetworkBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "on", "1", "true":
		return true, nil
	case "no", "off", "0", "false":
		return false, nil
	default:
		return false, fmt.Errorf("unrecognized value %q", value)
	}
}
