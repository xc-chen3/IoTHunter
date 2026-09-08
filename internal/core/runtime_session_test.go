package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunLocalRuntimeUsesKnownCLIWithoutShell(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(binDir, "codex")
	contents := "#!/bin/sh\nprintf 'runtime-ok:%s\\n' \"$3\"\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	result := RunLocalRuntime(context.Background(), "codex", "inspect target")
	if result.Status != "completed" {
		t.Fatalf("runtime status = %s, error = %s", result.Status, result.Error)
	}
	if result.Output != "runtime-ok:inspect target" {
		t.Fatalf("runtime output = %q", result.Output)
	}
}
