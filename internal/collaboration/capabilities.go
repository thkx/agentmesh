package collaboration

import (
	"sort"
	"strings"
)

func normalizeCapabilityList(values []string, maxItems int) ([]string, error) {
	if len(values) > maxItems {
		return nil, ErrInvalid
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || len(value) > 64 {
			return nil, ErrInvalid
		}
		for _, r := range value {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
				continue
			}
			return nil, ErrInvalid
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func hasRequiredCapabilities(provided, required []string) bool {
	if len(required) == 0 {
		return true
	}
	available := make(map[string]struct{}, len(provided))
	for _, capability := range provided {
		available[strings.ToLower(strings.TrimSpace(capability))] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := available[capability]; !ok {
			return false
		}
	}
	return true
}

func sameCapabilities(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
