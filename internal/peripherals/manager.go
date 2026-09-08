// Package peripherals owns real laboratory device handles. The control plane
// never exposes a driver handle to an Agent; callers receive an expiring
// session and every write is checked against its lease mode.
package peripherals

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Descriptor struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Name     string         `json:"name"`
	Driver   string         `json:"driver,omitempty"`
	Endpoint string         `json:"endpoint,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Endpoint struct {
	Address string
	Port    string
}

type Session struct {
	ID           string    `json:"session_id"`
	WorkspaceID  string    `json:"workspace_id,omitempty"`
	PeripheralID string    `json:"peripheral_id"`
	Kind         string    `json:"kind"`
	OwnerType    string    `json:"owner_type"`
	OwnerID      string    `json:"owner_id"`
	Mode         string    `json:"mode"`
	Status       string    `json:"status"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Session returns a current lease snapshot without exposing the underlying
// handle. Callers use it to associate a command with a persisted peripheral.
func (m *Manager) Session(sessionID string) (Session, error) {
	state, err := m.session(sessionID)
	if err != nil {
		return Session{}, err
	}
	return state.Session, nil
}

type OpenRequest struct {
	WorkspaceID  string
	PeripheralID string
	Kind         string
	Endpoint     Endpoint
	Config       map[string]any
	OwnerType    string
	OwnerID      string
	Mode         string
	LeaseTTL     time.Duration
}

type InvokeRequest struct {
	SessionID string
	Command   string
	Args      map[string]any
}

type InvokeResult struct {
	Status  string         `json:"status"`
	Data    map[string]any `json:"data,omitempty"`
	Bytes   []byte         `json:"bytes,omitempty"`
	Message string         `json:"message,omitempty"`
}

type Telemetry struct {
	At      time.Time      `json:"at"`
	Topic   string         `json:"topic,omitempty"`
	Command string         `json:"command"`
	Status  string         `json:"status"`
	Data    map[string]any `json:"data,omitempty"`
	Bytes   []byte         `json:"bytes,omitempty"`
	Error   string         `json:"error,omitempty"`
}

type Handle interface {
	io.ReadWriteCloser
	Identity(context.Context) (map[string]any, error)
	Configure(context.Context, map[string]any) error
}

type Adapter interface {
	Kind() string
	Discover(context.Context) ([]Descriptor, error)
	Open(context.Context, Endpoint, map[string]any) (Handle, error)
}

type sessionState struct {
	Session
	handle    Handle
	telemetry []Telemetry
	opMu      sync.Mutex
	subs      map[uint64]telemetrySubscription
}

type telemetrySubscription struct {
	ch     chan Telemetry
	topics map[string]struct{}
}

type Manager struct {
	mu                sync.Mutex
	adapters          map[string]Adapter
	sessions          map[string]*sessionState
	observer          func(Session, string)
	telemetryObserver func(Session, Telemetry)
	nextSub           uint64
}

func NewManager() *Manager {
	m := &Manager{adapters: map[string]Adapter{}, sessions: map[string]*sessionState{}}
	serial := NewSerialAdapter()
	m.Register(serial)
	for _, kind := range []string{"tcp", "scpi", "power", "scope", "packet"} {
		m.Register(NewTCPAdapter(kind))
	}
	// J-Link and Bluetooth driver hosts use the same bounded TCP transport
	// until a vendor SDK adapter is installed. This keeps the manager contract
	// usable with a local driver host without embedding an SDK in the client.
	for _, kind := range []string{"jlink", "bluetooth"} {
		m.Register(NewTCPAdapter(kind))
	}
	return m
}

// SetObserver lets the control plane persist lease lifecycle events. The
// callback is always invoked after the manager lock is released.
func (m *Manager) SetObserver(observer func(Session, string)) {
	m.mu.Lock()
	m.observer = observer
	m.mu.Unlock()
}

// SetTelemetryObserver registers a control-plane projection callback. The
// callback is invoked asynchronously after the in-memory record and bounded
// subscriptions have been updated, so persistence cannot block a device
// command or a slow subscriber.
func (m *Manager) SetTelemetryObserver(observer func(Session, Telemetry)) {
	m.mu.Lock()
	m.telemetryObserver = observer
	m.mu.Unlock()
}

func (m *Manager) Register(adapter Adapter) {
	if adapter == nil || strings.TrimSpace(adapter.Kind()) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adapters[strings.ToLower(adapter.Kind())] = adapter
}

func (m *Manager) Discover(ctx context.Context, kind string) ([]Descriptor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if kind != "" {
		adapter, ok := m.adapters[strings.ToLower(kind)]
		if !ok {
			return nil, fmt.Errorf("peripheral adapter %q is not registered", kind)
		}
		return adapter.Discover(ctx)
	}
	var out []Descriptor
	for _, adapter := range m.adapters {
		items, err := adapter.Discover(ctx)
		if err != nil {
			continue
		}
		out = append(out, items...)
	}
	return out, nil
}

func (m *Manager) Connect(ctx context.Context, request OpenRequest) (Session, error) {
	kind := strings.ToLower(strings.TrimSpace(request.Kind))
	if kind == "" || strings.TrimSpace(request.PeripheralID) == "" {
		return Session{}, errors.New("peripheral_id and kind are required")
	}
	ownerType, ownerID := request.OwnerType, request.OwnerID
	if ownerType == "" {
		ownerType = "user"
	}
	if ownerID == "" {
		ownerID = "desktop"
	}
	mode := request.Mode
	if mode == "" {
		mode = "exclusive"
	}
	if mode != "exclusive" && mode != "shared-read" {
		return Session{}, fmt.Errorf("unsupported lease mode %q", mode)
	}
	if err := ValidateConfig(kind, request.Config); err != nil {
		return Session{}, err
	}
	ttl := request.LeaseTTL
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 30 * time.Minute
	}
	m.mu.Lock()
	adapter, ok := m.adapters[kind]
	if !ok {
		m.mu.Unlock()
		return Session{}, fmt.Errorf("peripheral adapter %q is not registered", kind)
	}
	expired := make([]*sessionState, 0)
	for _, current := range m.sessions {
		if current.ExpiresAt.Before(time.Now()) {
			current.Status = "expired"
			expired = append(expired, current)
			continue
		}
		if current.PeripheralID != request.PeripheralID {
			continue
		}
		if mode == "shared-read" && current.Mode == "shared-read" {
			continue
		}
		m.mu.Unlock()
		return Session{}, fmt.Errorf("peripheral is leased by %s/%s", current.OwnerType, current.OwnerID)
	}
	for _, current := range expired {
		delete(m.sessions, current.ID)
		closeTelemetrySubscriptions(current)
	}
	observer := m.observer
	m.mu.Unlock()
	for _, current := range expired {
		current.opMu.Lock()
		_ = current.handle.Close()
		current.opMu.Unlock()
		if observer != nil {
			observer(current.Session, "expired")
		}
	}
	handle, err := adapter.Open(ctx, request.Endpoint, request.Config)
	if err != nil {
		return Session{}, err
	}
	session := Session{ID: newSessionID(), WorkspaceID: request.WorkspaceID, PeripheralID: request.PeripheralID, Kind: kind, OwnerType: ownerType, OwnerID: ownerID, Mode: mode, Status: "connected", ExpiresAt: time.Now().Add(ttl)}
	m.mu.Lock()
	for _, current := range m.sessions {
		if current.PeripheralID != request.PeripheralID || current.ExpiresAt.Before(time.Now()) {
			continue
		}
		if mode == "shared-read" && current.Mode == "shared-read" {
			continue
		}
		m.mu.Unlock()
		_ = handle.Close()
		return Session{}, fmt.Errorf("peripheral is leased by %s/%s", current.OwnerType, current.OwnerID)
	}
	m.sessions[session.ID] = &sessionState{Session: session, handle: handle, subs: map[uint64]telemetrySubscription{}}
	m.mu.Unlock()
	return session, nil
}

func (m *Manager) Disconnect(ctx context.Context, sessionID string) error {
	_ = ctx
	m.mu.Lock()
	state, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
		closeTelemetrySubscriptions(state)
	}
	m.mu.Unlock()
	if !ok {
		return errors.New("session not found")
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	err := state.handle.Close()
	if m.observer != nil {
		m.observer(state.Session, "disconnected")
	}
	return err
}

func (m *Manager) Sessions() []Session {
	m.mu.Lock()
	out := make([]Session, 0, len(m.sessions))
	now := time.Now()
	expired := make([]*sessionState, 0)
	for id, state := range m.sessions {
		if state.ExpiresAt.Before(now) {
			state.Status = "expired"
			expired = append(expired, state)
			delete(m.sessions, id)
			closeTelemetrySubscriptions(state)
			continue
		}
		out = append(out, state.Session)
	}
	observer := m.observer
	m.mu.Unlock()
	for _, state := range expired {
		state.opMu.Lock()
		_ = state.handle.Close()
		state.opMu.Unlock()
	}
	if observer != nil {
		for _, state := range expired {
			observer(state.Session, "expired")
		}
	}
	return out
}

func (m *Manager) GetConfig(ctx context.Context, sessionID string) (map[string]any, error) {
	state, err := m.session(sessionID)
	if err != nil {
		return nil, err
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	if configurable, ok := state.handle.(interface {
		CurrentConfig(context.Context) (map[string]any, error)
	}); ok {
		return configurable.CurrentConfig(ctx)
	}
	return state.handle.Identity(ctx)
}

func (m *Manager) ApplyConfig(ctx context.Context, sessionID string, config map[string]any) error {
	state, err := m.session(sessionID)
	if err != nil {
		return err
	}
	if state.Mode == "shared-read" {
		return errors.New("shared-read session cannot change peripheral configuration")
	}
	if err := ValidateConfig(state.Kind, config); err != nil {
		return err
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	if err := state.handle.Configure(ctx, config); err != nil {
		m.record(sessionID, Telemetry{At: time.Now().UTC(), Command: "configure", Status: "failed", Data: config, Error: err.Error()})
		return err
	}
	m.record(sessionID, Telemetry{At: time.Now().UTC(), Command: "configure", Status: "completed", Data: config})
	return nil
}

func (m *Manager) Invoke(ctx context.Context, request InvokeRequest) (InvokeResult, error) {
	state, err := m.session(request.SessionID)
	if err != nil {
		return InvokeResult{}, err
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	command := strings.ToLower(strings.TrimSpace(request.Command))
	switch command {
	case "identity":
		identity, err := state.handle.Identity(ctx)
		if err != nil {
			m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: "failed", Error: err.Error()})
			return InvokeResult{}, err
		}
		result := InvokeResult{Status: "completed", Data: identity}
		m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: result.Status, Data: identity})
		return result, nil
	case "read":
		maxBytes := intArg(request.Args, "max_bytes", 4096)
		if maxBytes < 1 || maxBytes > 1<<20 {
			return InvokeResult{}, errors.New("max_bytes must be between 1 and 1048576")
		}
		buf := make([]byte, maxBytes)
		n, err := state.handle.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: "failed", Error: err.Error()})
			return InvokeResult{}, err
		}
		result := InvokeResult{Status: "completed", Bytes: append([]byte(nil), buf[:n]...), Data: map[string]any{"bytes_read": n, "encoding": "base64"}}
		m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: result.Status, Data: result.Data, Bytes: result.Bytes})
		return result, nil
	case "write":
		if state.Mode == "shared-read" {
			return InvokeResult{}, errors.New("shared-read session cannot write")
		}
		payload, err := payloadArg(request.Args)
		if err != nil {
			return InvokeResult{}, err
		}
		n, err := state.handle.Write(payload)
		if err != nil {
			m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: "failed", Error: err.Error()})
			return InvokeResult{}, err
		}
		result := InvokeResult{Status: "completed", Data: map[string]any{"bytes_written": n}}
		m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: result.Status, Data: result.Data})
		return result, nil
	case "drain":
		if state.Mode == "shared-read" {
			return InvokeResult{}, errors.New("shared-read session cannot drain output")
		}
		if drainer, ok := state.handle.(interface{ Drain() error }); ok {
			if err := drainer.Drain(); err != nil {
				m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: "failed", Error: err.Error()})
				return InvokeResult{}, err
			}
		}
		result := InvokeResult{Status: "completed"}
		m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: result.Status})
		return result, nil
	case "query", "scpi", "measure", "capture", "attach", "bluetooth_capture", "scan", "reset", "halt", "read_memory":
		if state.Mode == "shared-read" && command != "measure" && command != "capture" {
			return InvokeResult{}, errors.New("shared-read session cannot issue control queries")
		}
		query := stringArg(request.Args, "query", "")
		if query == "" {
			switch command {
			case "measure":
				query = "MEAS:ALL?"
			case "capture":
				query = "WAV:DATA?"
			case "attach":
				query = "ATTACH"
			case "bluetooth_capture":
				query = "CAPTURE"
			case "scan":
				query = "SCAN"
			case "reset":
				query = "RESET"
			case "halt":
				query = "HALT"
			case "read_memory":
				address := numberArg(request.Args, "address")
				length := intArg(request.Args, "length", 0)
				if address == "" || length < 1 || length > 1<<20 {
					return InvokeResult{}, errors.New("read_memory requires address and length between 1 and 1048576")
				}
				query = fmt.Sprintf("READ_MEMORY %s %d", address, length)
			}
		}
		if query == "" {
			return InvokeResult{}, errors.New("query is required")
		}
		if !strings.HasSuffix(query, "\n") {
			query += "\n"
		}
		if _, err := state.handle.Write([]byte(query)); err != nil {
			m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: "failed", Error: err.Error()})
			return InvokeResult{}, err
		}
		maxBytes := intArg(request.Args, "max_bytes", 64*1024)
		if maxBytes < 1 || maxBytes > 8<<20 {
			return InvokeResult{}, errors.New("max_bytes must be between 1 and 8388608")
		}
		buf := make([]byte, maxBytes)
		n, err := state.handle.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: "failed", Error: err.Error()})
			return InvokeResult{}, err
		}
		result := InvokeResult{Status: "completed", Bytes: append([]byte(nil), buf[:n]...), Data: map[string]any{"bytes_read": n, "query": strings.TrimSpace(query), "encoding": "base64"}}
		m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: result.Status, Data: result.Data, Bytes: result.Bytes})
		return result, nil
	case "cycle":
		if state.Mode == "shared-read" {
			return InvokeResult{}, errors.New("shared-read session cannot change instrument state")
		}
		if _, err := state.handle.Write([]byte("OUTP OFF\n")); err != nil {
			return InvokeResult{}, err
		}
		delay := intArg(request.Args, "off_ms", 100)
		if delay < 0 || delay > 60000 {
			return InvokeResult{}, errors.New("off_ms must be between 0 and 60000")
		}
		if delay > 0 {
			timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return InvokeResult{}, ctx.Err()
			}
		}
		if _, err := state.handle.Write([]byte("OUTP ON\n")); err != nil {
			return InvokeResult{}, err
		}
		result := InvokeResult{Status: "completed", Data: map[string]any{"off_ms": delay}}
		m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: result.Status, Data: result.Data})
		return result, nil
	case "set_voltage", "set_current", "output":
		if state.Mode == "shared-read" {
			return InvokeResult{}, errors.New("shared-read session cannot change instrument state")
		}
		commandText := stringArg(request.Args, "command", "")
		if commandText == "" {
			switch command {
			case "set_voltage":
				commandText = fmt.Sprintf("VOLT %s", numberArg(request.Args, "voltage"))
			case "set_current":
				commandText = fmt.Sprintf("CURR %s", numberArg(request.Args, "current"))
			case "output":
				enabled, ok := boolArg(request.Args, "enabled")
				if !ok {
					return InvokeResult{}, errors.New("enabled is required")
				}
				if enabled {
					commandText = "OUTP ON"
				} else {
					commandText = "OUTP OFF"
				}
			}
		}
		if strings.TrimSpace(commandText) == "" {
			return InvokeResult{}, errors.New("instrument command is required")
		}
		if !strings.HasSuffix(commandText, "\n") {
			commandText += "\n"
		}
		if _, err := state.handle.Write([]byte(commandText)); err != nil {
			m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: "failed", Error: err.Error()})
			return InvokeResult{}, err
		}
		result := InvokeResult{Status: "completed", Data: map[string]any{"command": strings.TrimSpace(commandText)}}
		m.record(request.SessionID, Telemetry{At: time.Now().UTC(), Command: command, Status: result.Status, Data: result.Data})
		return result, nil
	default:
		return InvokeResult{}, fmt.Errorf("unsupported peripheral command %q", command)
	}
}

func (m *Manager) record(sessionID string, event Telemetry) {
	m.mu.Lock()
	state, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if event.Topic == "" {
		event.Topic = eventTopic(event)
	}
	state.telemetry = append(state.telemetry, event)
	if len(state.telemetry) > 1000 {
		state.telemetry = state.telemetry[len(state.telemetry)-1000:]
	}
	for _, subscription := range state.subs {
		if len(subscription.topics) > 0 {
			if _, matched := subscription.topics[event.Topic]; !matched {
				continue
			}
		}
		select {
		case subscription.ch <- event:
		default:
			// Slow consumers cannot block a physical-device command.
		}
	}
	observer := m.telemetryObserver
	session := state.Session
	m.mu.Unlock()
	if observer != nil {
		// The observer may write SQLite or an artifact. Keep the device path
		// responsive even when the storage backend is busy.
		go observer(session, event)
	}
}

func (m *Manager) Telemetry(sessionID string) ([]Telemetry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.sessions[sessionID]
	if !ok {
		return nil, errors.New("session not found")
	}
	if state.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("session lease expired")
	}
	return append([]Telemetry(nil), state.telemetry...), nil
}

// Subscribe returns live, bounded telemetry for a session. An empty topic
// list receives all records; otherwise topics are matched against the
// normalized values produced by eventTopic.
func (m *Manager) Subscribe(ctx context.Context, sessionID string, topics []string) (<-chan Telemetry, error) {
	state, err := m.session(sessionID)
	if err != nil {
		return nil, err
	}
	filters := map[string]struct{}{}
	for _, topic := range topics {
		if value := strings.TrimSpace(topic); value != "" {
			filters[value] = struct{}{}
		}
	}
	channel := make(chan Telemetry, 64)
	m.mu.Lock()
	m.nextSub++
	subID := m.nextSub
	if state.subs == nil {
		state.subs = map[uint64]telemetrySubscription{}
	}
	state.subs[subID] = telemetrySubscription{ch: channel, topics: filters}
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		if subscription, ok := state.subs[subID]; ok {
			delete(state.subs, subID)
			close(subscription.ch)
		}
		m.mu.Unlock()
	}()
	return channel, nil
}

func closeTelemetrySubscriptions(state *sessionState) {
	for id, subscription := range state.subs {
		close(subscription.ch)
		delete(state.subs, id)
	}
}

func eventTopic(event Telemetry) string {
	if event.Topic != "" {
		return event.Topic
	}
	if event.Data != nil {
		if topic, ok := event.Data["topic"].(string); ok && strings.TrimSpace(topic) != "" {
			return topic
		}
	}
	switch event.Command {
	case "measure":
		return "power.measurement"
	case "capture":
		return "scope.waveform"
	case "read":
		return "serial.rx"
	case "write":
		return "serial.tx"
	default:
		return "peripheral." + event.Command
	}
}

// TelemetryTopic returns the normalized topic used by subscriptions and the
// control-plane event stream.
func TelemetryTopic(event Telemetry) string { return eventTopic(event) }

func (m *Manager) session(id string) (*sessionState, error) {
	m.mu.Lock()
	state, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return nil, errors.New("session not found")
	}
	if state.ExpiresAt.Before(time.Now()) {
		delete(m.sessions, id)
		state.Status = "expired"
		closeTelemetrySubscriptions(state)
		observer := m.observer
		m.mu.Unlock()
		state.opMu.Lock()
		_ = state.handle.Close()
		state.opMu.Unlock()
		if observer != nil {
			observer(state.Session, "expired")
		}
		return nil, errors.New("session lease expired")
	}
	m.mu.Unlock()
	return state, nil
}

func payloadArg(args map[string]any) ([]byte, error) {
	if args == nil {
		return nil, errors.New("write requires data or data_base64")
	}
	if encoded, ok := args["data_base64"].(string); ok && encoded != "" {
		payload, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode data_base64: %w", err)
		}
		return payload, nil
	}
	if data, ok := args["data"].(string); ok {
		return []byte(data), nil
	}
	return nil, errors.New("write requires data or data_base64")
}

func intArg(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	if value, ok := args[key].(float64); ok {
		return int(value)
	}
	if value, ok := args[key].(int); ok {
		return value
	}
	return fallback
}

func stringArg(args map[string]any, key, fallback string) string {
	if args == nil {
		return fallback
	}
	if value, ok := args[key].(string); ok {
		return value
	}
	return fallback
}

func numberArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	switch value := args[key].(type) {
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(value), 'f', -1, 32)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case string:
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func boolArg(args map[string]any, key string) (bool, bool) {
	if args == nil {
		return false, false
	}
	switch value := args[key].(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(value)
		return parsed, err == nil
	default:
		return false, false
	}
}

func newSessionID() string {
	return fmt.Sprintf("PS-%d", time.Now().UnixNano())
}
