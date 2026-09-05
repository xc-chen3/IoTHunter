package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type runtimeSpec struct {
	id           string
	name         string
	provider     string
	command      string
	candidates   []string
	capabilities []string
}

var localRuntimeSpecs = []runtimeSpec{
	{id: "claude", name: "Claude Code", provider: "anthropic", command: "claude", candidates: []string{".local/bin/claude"}, capabilities: []string{"chat", "agent", "code"}},
	{id: "codex", name: "Codex CLI", provider: "openai", command: "codex", candidates: []string{".local/bin/codex"}, capabilities: []string{"chat", "agent", "code", "review"}},
	{id: "grok", name: "Grok CLI", provider: "xai", command: "grok", candidates: []string{".grok/bin/grok", ".local/bin/grok"}, capabilities: []string{"chat", "agent", "code"}},
	{id: "kiro", name: "Kiro CLI", provider: "aws", command: "kiro-cli", candidates: []string{".local/bin/kiro-cli", ".kiro/bin/kiro-cli"}, capabilities: []string{"chat", "agent", "code"}},
}

func localRuntimeSpec(id string) (runtimeSpec, bool) {
	for _, spec := range localRuntimeSpecs {
		if spec.id == id {
			return spec, true
		}
	}
	return runtimeSpec{}, false
}

func runtimePath(spec runtimeSpec) string {
	if home, err := os.UserHomeDir(); err == nil {
		for _, candidate := range spec.candidates {
			path := filepath.Join(home, candidate)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	if path, err := exec.LookPath(spec.command); err == nil {
		if absolute, err := filepath.Abs(path); err == nil {
			return absolute
		}
		return path
	}
	return ""
}

func discoverLocalRuntimes(ctx context.Context) []LocalRuntime {
	out := make([]LocalRuntime, 0, len(localRuntimeSpecs))
	for _, spec := range localRuntimeSpecs {
		runtime := LocalRuntime{ID: spec.id, Name: spec.name, Provider: spec.provider, Command: spec.command, Capabilities: append([]string(nil), spec.capabilities...), AuthState: "unknown", LastChecked: now()}
		runtime.Path = runtimePath(spec)
		if runtime.Path == "" {
			runtime.Status = "missing"
			runtime.Error = "command not found on host"
			out = append(out, runtime)
			continue
		}
		runtime.Available = true
		runtime.Status = "available"
		version, err := runRuntimeCommand(ctx, runtime.Path, "--version")
		if err != nil {
			runtime.Status = "error"
			runtime.Error = safeRuntimeError(err)
		} else {
			runtime.Version = firstOutputLine(version)
		}
		out = append(out, runtime)
	}
	return out
}

func probeLocalRuntime(ctx context.Context, id string) (LocalRuntime, string, error) {
	spec, ok := localRuntimeSpec(id)
	if !ok {
		return LocalRuntime{}, "", fmt.Errorf("unknown runtime %q", id)
	}
	runtimes := discoverLocalRuntimes(ctx)
	var runtime LocalRuntime
	for _, item := range runtimes {
		if item.ID == id {
			runtime = item
			break
		}
	}
	if runtime.Path == "" {
		return runtime, "", fmt.Errorf("%s is not installed", spec.name)
	}
	help, err := runRuntimeCommand(ctx, runtime.Path, "--help")
	if err != nil {
		return runtime, "", err
	}
	return runtime, trimOutput(help, 4000), nil
}

func runRuntimeCommand(parent context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	output = []byte(trimOutput(string(output), 1000))
	if err != nil {
		if len(output) > 0 {
			return string(output), fmt.Errorf("%w: %s", err, firstOutputLine(string(output)))
		}
		return string(output), err
	}
	return string(output), nil
}

func firstOutputLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return trimOutput(line, 180)
		}
	}
	return ""
}

func trimOutput(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func safeRuntimeError(err error) string {
	if err == nil {
		return ""
	}
	return trimOutput(err.Error(), 240)
}
