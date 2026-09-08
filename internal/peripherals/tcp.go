package peripherals

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// tcpAdapter provides a small line/raw transport for SCPI instruments and
// driver hosts that expose a TCP socket. Protocol-specific commands still go
// through Capability permissions and the Manager lease.
type tcpAdapter struct{ kind string }

func NewTCPAdapter(kind string) Adapter {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "tcp"
	}
	return tcpAdapter{kind: kind}
}

func (a tcpAdapter) Kind() string { return a.kind }

func (a tcpAdapter) Discover(ctx context.Context) ([]Descriptor, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	// TCP instruments cannot be safely discovered by scanning a network. They
	// are registered with an explicit host:port endpoint by the user.
	return []Descriptor{}, nil
}

func (a tcpAdapter) Open(ctx context.Context, endpoint Endpoint, config map[string]any) (Handle, error) {
	address := strings.TrimSpace(endpoint.Address)
	if address == "" {
		address = strings.TrimSpace(endpoint.Port)
	}
	if address == "" {
		return nil, fmt.Errorf("%s endpoint host:port is required", a.kind)
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		// Permit a bare host only when an explicit port was supplied separately.
		portText := strings.TrimSpace(endpoint.Port)
		if portText == "" {
			port := intConfig(config, "port", 0)
			if port > 0 && port <= 65535 {
				portText = strconv.Itoa(port)
			}
		}
		port, parseErr := strconv.Atoi(portText)
		if parseErr != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("invalid TCP endpoint %q", address)
		}
		address = net.JoinHostPort(address, portText)
	}
	timeout := durationConfig(config, "connect_timeout_ms", 5000)
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", address, err)
	}
	readTimeout := durationConfig(config, "read_timeout_ms", 1000)
	return &tcpHandle{conn: conn, endpoint: address, kind: a.kind, readTimeout: readTimeout, config: cloneConfig(config)}, nil
}

type tcpHandle struct {
	conn        net.Conn
	endpoint    string
	kind        string
	readTimeout time.Duration
	config      map[string]any
}

func (h *tcpHandle) Read(p []byte) (int, error) {
	if h.readTimeout > 0 {
		_ = h.conn.SetReadDeadline(time.Now().Add(h.readTimeout))
	}
	return h.conn.Read(p)
}

func (h *tcpHandle) Write(p []byte) (int, error) { return h.conn.Write(p) }
func (h *tcpHandle) Close() error                { return h.conn.Close() }

func (h *tcpHandle) Identity(context.Context) (map[string]any, error) {
	identity := cloneConfig(h.config)
	identity["transport"], identity["kind"], identity["endpoint"] = "tcp", h.kind, h.endpoint
	identity["read_timeout_ms"] = h.readTimeout.Milliseconds()
	return identity, nil
}

func (h *tcpHandle) CurrentConfig(context.Context) (map[string]any, error) {
	return cloneConfig(h.config), nil
}

func (h *tcpHandle) Configure(ctx context.Context, config map[string]any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	h.readTimeout = durationConfig(config, "read_timeout_ms", int(h.readTimeout.Milliseconds()))
	h.config = cloneConfig(config)
	return nil
}
