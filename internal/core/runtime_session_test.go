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
	contents := `#!/bin/sh
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output-last-message" ]; then
    shift
    output="$1"
  fi
  shift
done
printf 'runtime-ok:inspect target\n' > "$output"
printf 'transport-noise\n' >&2
printf 'fallback-output\n'
`
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
