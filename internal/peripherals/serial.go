package peripherals

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"
)

type serialAdapter struct{}

func NewSerialAdapter() Adapter { return serialAdapter{} }

func (serialAdapter) Kind() string { return "serial" }

func (serialAdapter) Discover(ctx context.Context) ([]Descriptor, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	out := make([]Descriptor, 0, len(ports))
	for _, port := range ports {
		out = append(out, Descriptor{ID: "serial:" + port, Kind: "serial", Name: port, Driver: "go.bug.st/serial", Endpoint: port, Metadata: map[string]any{"port": port}})
	}
	return out, nil
}

func (serialAdapter) Open(ctx context.Context, endpoint Endpoint, config map[string]any) (Handle, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	portName := strings.TrimSpace(endpoint.Port)
	if portName == "" {
		portName = strings.TrimSpace(endpoint.Address)
	}
	if portName == "" {
		return nil, fmt.Errorf("serial endpoint is required")
	}
	mode := serialMode(config)
	port, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial %s: %w", portName, err)
	}
	timeout := durationConfig(config, "read_timeout_ms", 1000)
	if err := port.SetReadTimeout(timeout); err != nil {
		_ = port.Close()
		return nil, fmt.Errorf("configure serial timeout: %w", err)
	}
	return &serialHandle{port: port, endpoint: portName, mode: mode, config: cloneConfig(config)}, nil
}

type serialHandle struct {
	port     serial.Port
	endpoint string
	mode     *serial.Mode
	config   map[string]any
}

func (h *serialHandle) Read(p []byte) (int, error)  { return h.port.Read(p) }
func (h *serialHandle) Write(p []byte) (int, error) { return h.port.Write(p) }
func (h *serialHandle) Close() error                { return h.port.Close() }
func (h *serialHandle) Drain() error                { return h.port.Drain() }

func (h *serialHandle) Identity(context.Context) (map[string]any, error) {
	identity := cloneConfig(h.config)
	identity["transport"], identity["port"] = "serial", h.endpoint
	identity["baud_rate"], identity["data_bits"], identity["stop_bits"], identity["parity"] = h.mode.BaudRate, h.mode.DataBits, h.mode.StopBits, h.mode.Parity
	return identity, nil
}

func (h *serialHandle) CurrentConfig(context.Context) (map[string]any, error) {
	return cloneConfig(h.config), nil
}

func (h *serialHandle) Configure(ctx context.Context, config map[string]any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	mode := serialMode(config)
	if err := h.port.SetMode(mode); err != nil {
		return err
	}
	h.mode = mode
	h.config = cloneConfig(config)
	return nil
}

func cloneConfig(config map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range config {
		out[key] = value
	}
	return out
}

func serialMode(config map[string]any) *serial.Mode {
	mode := &serial.Mode{BaudRate: intConfig(config, "baud_rate", 115200), DataBits: intConfig(config, "data_bits", 8), Parity: serial.NoParity, StopBits: serial.OneStopBit}
	switch strings.ToLower(stringConfig(config, "parity", "none")) {
	case "odd":
		mode.Parity = serial.OddParity
	case "even":
		mode.Parity = serial.EvenParity
	}
	switch intConfig(config, "stop_bits", 1) {
	case 2:
		mode.StopBits = serial.TwoStopBits
	case 3:
		mode.StopBits = serial.OnePointFiveStopBits
	}
	return mode
}

func intConfig(config map[string]any, key string, fallback int) int {
	if config == nil {
		return fallback
	}
	switch value := config[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func stringConfig(config map[string]any, key, fallback string) string {
	if config == nil {
		return fallback
	}
	if value, ok := config[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func durationConfig(config map[string]any, key string, fallback int) time.Duration {
	value := intConfig(config, key, fallback)
	if value < 0 {
		value = 0
	}
	return time.Duration(value) * time.Millisecond
}
