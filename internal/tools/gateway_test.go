package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGatewayUsesAllowlistedExecutableAndPermissionIntersection(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "tool")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf 'tool:%s' \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gateway := NewGateway()
	if err := gateway.Register(Definition{Name: "test.tool", Path: command, Permissions: PermissionSet{Filesystem: "workspace-readonly"}}); err != nil {
		t.Fatal(err)
	}
	result, err := gateway.Run(context.Background(), Request{Tool: "test.tool", Args: []string{"ok"}, Permissions: PermissionSet{Filesystem: "workspace-readonly"}})
	if err != nil || result.Status != "completed" || result.Output != "tool:ok" {
		t.Fatalf("unexpected result: %+v, err=%v", result, err)
	}
	if _, err := gateway.Run(context.Background(), Request{Tool: "test.tool", Permissions: PermissionSet{Filesystem: "workspace-write"}}); err == nil {
		t.Fatal("expected write permission denial")
	}
}

func TestGatewayRequiresContainerImageAndConstrainsWorkspace(t *testing.T) {
	gateway := NewGateway()
	if err := gateway.Register(Definition{Name: "container.tool", Path: "/usr/bin/tool", Isolation: "docker"}); err == nil {
		t.Fatal("expected container image validation")
	}
	dir := t.TempDir()
	command := filepath.Join(dir, "tool")
	if err := os.WriteFile(command, []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Register(Definition{Name: "test.tool", Path: command, Permissions: PermissionSet{Filesystem: "workspace-readonly"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.Run(context.Background(), Request{Tool: "test.tool", WorkingDir: t.TempDir(), WorkspaceRoot: dir, Permissions: PermissionSet{Filesystem: "workspace-readonly"}}); err == nil {
		t.Fatal("expected working directory boundary denial")
	}
}
