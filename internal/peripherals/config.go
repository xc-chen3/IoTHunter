package peripherals

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateConfig enforces the driver-level parameter contract before a
// preset is persisted or applied to a live session. Safety limits are kept
// separate from instrument settings and are validated by the control plane.
func ValidateConfig(kind string, config map[string]any) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if config == nil {
		return nil
	}
	switch kind {
	case "serial", "uart":
		baud := intConfig(config, "baud_rate", 115200)
		if baud < 50 || baud > 4000000 {
			return fmt.Errorf("baud_rate must be between 50 and 4000000")
		}
		bits := intConfig(config, "data_bits", 8)
		if bits < 5 || bits > 8 {
			return fmt.Errorf("data_bits must be between 5 and 8")
		}
		stop := intConfig(config, "stop_bits", 1)
		if stop < 1 || stop > 3 {
			return fmt.Errorf("stop_bits must be 1, 2, or 3")
		}
		parity := strings.ToLower(stringConfig(config, "parity", "none"))
		if parity != "none" && parity != "odd" && parity != "even" {
			return fmt.Errorf("parity must be none, odd, or even")
		}
	case "tcp", "scpi", "power", "scope", "jlink", "bluetooth", "packet":
		if port := intConfig(config, "port", 0); port != 0 && (port < 1 || port > 65535) {
			return fmt.Errorf("port must be between 1 and 65535")
		}
		if value, present := config["voltage"]; present {
			number, ok := numericConfig(value)
			if !ok || number < 0 || number > 1000 {
				return fmt.Errorf("voltage must be between 0 and 1000")
			}
		}
		if value, present := config["current"]; present {
			number, ok := numericConfig(value)
			if !ok || number < 0 || number > 100 {
				return fmt.Errorf("current must be between 0 and 100")
			}
		}
		if value, present := config["sample_rate"]; present {
			number, ok := numericConfig(value)
			if !ok || number <= 0 || number > 10e9 {
				return fmt.Errorf("sample_rate must be between 1 and 10000000000")
			}
		}
		if value, present := config["channel"]; present {
			if channel, ok := value.(string); ok && strings.TrimSpace(channel) == "" {
				return fmt.Errorf("channel must not be empty")
			}
		}
	default:
		return fmt.Errorf("unsupported peripheral kind %q", kind)
	}
	for _, key := range []string{"connect_timeout_ms", "read_timeout_ms"} {
		if value := intConfig(config, key, 0); value < 0 || value > 60000 {
			return fmt.Errorf("%s must be between 0 and 60000", key)
		}
	}
	return nil
}

func ValidateSafetyLimits(limits map[string]any) error {
	if limits == nil {
		return nil
	}
	for _, key := range []string{"max_voltage", "max_current"} {
		if value, present := limits[key]; present {
			number, ok := numericConfig(value)
			if !ok || number <= 0 {
				return fmt.Errorf("safety limit %s must be a positive number", key)
			}
		}
	}
	return nil
}

func numericConfig(value any) (float64, bool) {
	switch value := value.(type) {
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}
