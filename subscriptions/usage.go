package subscriptions

import (
	"strconv"
	"strings"
)

func parseUsage(header string) *Usage {
	if header == "" || len(header) > 1024 {
		return nil
	}
	usage := &Usage{}
	seen := make(map[string]bool, 4)
	for _, field := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			return nil
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		if seen[key] {
			return nil
		}
		seen[key] = true
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil || number < 0 {
			return nil
		}
		switch key {
		case "upload":
			usage.Upload = number
		case "download":
			usage.Download = number
		case "total":
			usage.Total = number
		case "expire":
			usage.ExpiresAt = number
		default:
			return nil
		}
	}
	if len(seen) == 0 {
		return nil
	}
	return usage
}
