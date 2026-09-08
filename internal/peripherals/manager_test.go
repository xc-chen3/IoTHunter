package peripherals

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

type fakeAdapter struct{}

func (fakeAdapter) Kind() string { return "fake" }
func (fakeAdapter) Discover(context.Context) ([]Descriptor, error) {
	return []Descriptor{{ID: "fake:1", Kind: "fake", Name: "fake", Endpoint: "fake:1"}}, nil
}
func (fakeAdapter) Open(context.Context, Endpoint, map[string]any) (Handle, error) {
	return &fakeHandle{}, nil
}

type fakeHandle struct{ bytes.Buffer }

func (h *fakeHandle) Close() error { return nil }
func (h *fakeHandle) Identity(context.Context) (map[string]any, error) {
	return map[string]any{"transport": "fake"}, nil
}
func (h *fakeHandle) Configure(context.Context, map[string]any) error { return nil }
func (h *fakeHandle) Read(p []byte) (int, error) {
	if h.Len() == 0 {
		return 0, io.EOF
	}
	return h.Buffer.Read(p)
}

func TestManagerEnforcesExclusiveLeaseAndWrite(t *testing.T) {
	m := NewManager()
	m.Register(fakeAdapter{})
	one, err := m.Connect(context.Background(), OpenRequest{PeripheralID: "P-1", Kind: "fake", Mode: "exclusive"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Connect(context.Background(), OpenRequest{PeripheralID: "P-1", Kind: "fake", Mode: "exclusive"}); err == nil {
		t.Fatal("expected exclusive lease conflict")
	}
	result, err := m.Invoke(context.Background(), InvokeRequest{SessionID: one.ID, Command: "write", Args: map[string]any{"data": "hello"}})
	if err != nil || result.Data["bytes_written"] != 5 {
		t.Fatalf("write result = %+v, err = %v", result, err)
	}
	if err := m.Disconnect(context.Background(), one.ID); err != nil {
		t.Fatal(err)
	}
}

func TestManagerSharedReadCannotWrite(t *testing.T) {
	m := NewManager()
	m.Register(fakeAdapter{})
	one, err := m.Connect(context.Background(), OpenRequest{PeripheralID: "P-1", Kind: "fake", Mode: "shared-read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(context.Background(), InvokeRequest{SessionID: one.ID, Command: "write", Args: map[string]any{"data": "x"}}); err == nil {
		t.Fatal("expected shared-read write denial")
	}
	second, err := m.Connect(context.Background(), OpenRequest{PeripheralID: "P-1", Kind: "fake", Mode: "shared-read"})
	if err != nil {
		t.Fatal(err)
	}
	_ = m.Disconnect(context.Background(), one.ID)
	_ = m.Disconnect(context.Background(), second.ID)
}

func TestManagerExpiresLeaseAndNotifiesObserver(t *testing.T) {
	m := NewManager()
	m.Register(fakeAdapter{})
	var observed Session
	var event string
	m.SetObserver(func(session Session, value string) { observed, event = session, value })
	session, err := m.Connect(context.Background(), OpenRequest{WorkspaceID: "W-1", PeripheralID: "P-1", Kind: "fake", LeaseTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sessions[session.ID].ExpiresAt = time.Now().Add(-time.Second)
	m.mu.Unlock()
	if sessions := m.Sessions(); len(sessions) != 0 {
		t.Fatalf("expired session remained visible: %+v", sessions)
	}
	if observed.ID != session.ID || observed.WorkspaceID != "W-1" || event != "expired" {
		t.Fatalf("observer = %+v, event = %s", observed, event)
	}
}

func TestManagerTelemetrySubscriptionReceivesAndFiltersEvents(t *testing.T) {
	m := NewManager()
	m.Register(fakeAdapter{})
	session, err := m.Connect(context.Background(), OpenRequest{PeripheralID: "P-sub", Kind: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := m.Subscribe(ctx, session.ID, []string{"serial.tx"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Invoke(context.Background(), InvokeRequest{SessionID: session.ID, Command: "write", Args: map[string]any{"data": "ok"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream:
		if event.Command != "write" {
			t.Fatalf("unexpected telemetry event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for telemetry")
	}
	cancel()
	select {
	case _, open := <-stream:
		if open {
			t.Fatal("telemetry stream remained open after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("telemetry stream did not close")
	}
	_ = m.Disconnect(context.Background(), session.ID)
}

func TestManagerTelemetryObserverReceivesTopicWithoutBlockingCommand(t *testing.T) {
	m := NewManager()
	m.Register(fakeAdapter{})
	observed := make(chan Telemetry, 1)
	m.SetTelemetryObserver(func(_ Session, event Telemetry) { observed <- event })
	session, err := m.Connect(context.Background(), OpenRequest{PeripheralID: "P-observer", Kind: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Disconnect(context.Background(), session.ID)
	if _, err := m.Invoke(context.Background(), InvokeRequest{SessionID: session.ID, Command: "write", Args: map[string]any{"data": "ping"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-observed:
		if event.Topic != "serial.tx" || event.Command != "write" || event.Status != "completed" {
			t.Fatalf("unexpected observed event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for telemetry observer")
	}
}
