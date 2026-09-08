package peripherals

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

type captureAdapter struct{ handle *captureHandle }

func (a *captureAdapter) Kind() string                                   { return "serial" }
func (a *captureAdapter) Discover(context.Context) ([]Descriptor, error) { return nil, nil }
func (a *captureAdapter) Open(context.Context, Endpoint, map[string]any) (Handle, error) {
	a.handle = &captureHandle{}
	return a.handle, nil
}

type captureHandle struct{ bytes.Buffer }

func (h *captureHandle) Close() error { return nil }
func (h *captureHandle) Identity(context.Context) (map[string]any, error) {
	return map[string]any{"model": "capture"}, nil
}
func (h *captureHandle) Configure(context.Context, map[string]any) error { return nil }
func (h *captureHandle) Read([]byte) (int, error)                        { return 0, io.EOF }

func TestManagerPowerCycleUsesSingleLeasedHandle(t *testing.T) {
	manager := NewManager()
	adapter := &captureAdapter{}
	manager.Register(adapter)
	session, err := manager.Connect(context.Background(), OpenRequest{PeripheralID: "P-cycle", Kind: "serial", Mode: "exclusive"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Invoke(context.Background(), InvokeRequest{SessionID: session.ID, Command: "cycle", Args: map[string]any{"off_ms": 0}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(adapter.handle.String()); got != "OUTP OFF\nOUTP ON" {
		t.Fatalf("cycle commands = %q", got)
	}
}

func TestManagerReadMemoryBuildsBoundedDriverCommand(t *testing.T) {
	manager := NewManager()
	adapter := &captureAdapter{}
	manager.Register(adapter)
	session, err := manager.Connect(context.Background(), OpenRequest{PeripheralID: "P-memory", Kind: "serial", Mode: "exclusive"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Invoke(context.Background(), InvokeRequest{SessionID: session.ID, Command: "read_memory", Args: map[string]any{"address": "0x20000000", "length": 16}}); err != nil {
		t.Fatal(err)
	}
	if got := adapter.handle.String(); !strings.Contains(got, "READ_MEMORY 0x20000000 16") {
		t.Fatalf("read memory command = %q", got)
	}
}
