package author

import (
	"fmt"
	"strconv"
	"strings"
)

type Bounds struct {
	MaxConcurrency     int
	MaxDurationSeconds int
	MaxTotalRequests   int
}

func DefaultBounds() Bounds {
	return Bounds{
		MaxConcurrency:     2000,
		MaxDurationSeconds: 3600,
		MaxTotalRequests:   1000000,
	}
}

func LoadBounds(getenv func(string) string) (Bounds, error) {
	bounds := DefaultBounds()
	settings := []struct {
		name  string
		value *int
	}{
		{name: "PRIMUS_AUTHOR_MAX_CONCURRENCY", value: &bounds.MaxConcurrency},
		{name: "PRIMUS_AUTHOR_MAX_DURATION_SECONDS", value: &bounds.MaxDurationSeconds},
		{name: "PRIMUS_AUTHOR_MAX_TOTAL_REQUESTS", value: &bounds.MaxTotalRequests},
	}
	for _, setting := range settings {
		raw := strings.TrimSpace(getenv(setting.name))
		if raw == "" {
			continue
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return Bounds{}, fmt.Errorf("%s must be a positive integer", setting.name)
		}
		*setting.value = parsed
	}
	return bounds, nil
}

func ValidateTestConfigSuggestion(suggestion TestConfigSuggestion, bounds Bounds) error {
	if bounds.MaxConcurrency < 1 || bounds.MaxDurationSeconds < 1 || bounds.MaxTotalRequests < 1 {
		return fmt.Errorf("test config generation limits must all be positive")
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(suggestion.Reasoning)), "clarification required:") {
		return fmt.Errorf("%s", strings.TrimSpace(suggestion.Reasoning))
	}
	if suggestion.Concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if suggestion.Concurrency > bounds.MaxConcurrency {
		return fmt.Errorf("concurrency %d exceeds maximum %d", suggestion.Concurrency, bounds.MaxConcurrency)
	}
	if strings.TrimSpace(suggestion.Reasoning) == "" {
		return fmt.Errorf("reasoning is required")
	}
	if len(suggestion.Reasoning) > 1000 {
		return fmt.Errorf("reasoning exceeds the 1000 character limit")
	}
	switch suggestion.Mode {
	case "load", "soak":
		if suggestion.TotalRequests != 0 {
			return fmt.Errorf("%s mode must not include total_requests", suggestion.Mode)
		}
		if suggestion.DurationSeconds < 1 {
			return fmt.Errorf("duration_seconds must be at least 1")
		}
		if suggestion.DurationSeconds > bounds.MaxDurationSeconds {
			return fmt.Errorf("duration_seconds %d exceeds maximum %d", suggestion.DurationSeconds, bounds.MaxDurationSeconds)
		}
	case "burst", "auth_probe":
		if suggestion.DurationSeconds != 0 {
			return fmt.Errorf("%s mode must not include duration_seconds", suggestion.Mode)
		}
		if suggestion.TotalRequests < 1 {
			return fmt.Errorf("total_requests must be at least 1")
		}
		if suggestion.TotalRequests > bounds.MaxTotalRequests {
			return fmt.Errorf("total_requests %d exceeds maximum %d", suggestion.TotalRequests, bounds.MaxTotalRequests)
		}
	default:
		return fmt.Errorf("mode %q is not supported", suggestion.Mode)
	}
	return nil
}
