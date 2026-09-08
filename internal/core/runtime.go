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

// RunLocalRuntime executes one explicitly requested, non-interactive prompt
// against a known local CLI. No shell is involved and output is bounded before
// it is returned to the control plane.
func RunLocalRuntime(ctx context.Context, id, prompt string) RuntimeResult {
	started := now()
	result := RuntimeResult{RuntimeID: id, Status: "failed", StartedAt: started}
	finish := func() RuntimeResult {
		completed := now()
		result.CompletedAt = &completed
		return result
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		result.Error = "prompt is required"
		return finish()
	}
	spec, ok := localRuntimeSpec(id)
	if !ok {
		result.Error = fmt.Sprintf("unknown runtime %q", id)
		return finish()
	}
	path := runtimePath(spec)
	if path == "" {
		result.Error = fmt.Sprintf("%s is not installed", spec.name)
		return finish()
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, path, runtimePromptArgs(id, prompt)...)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	result.Output = trimOutput(string(output), 64<<10)
	if runCtx.Err() != nil {
		result.Error = runCtx.Err().Error()
		return finish()
	}
	if err != nil {
		result.Error = safeRuntimeError(err)
		if result.Output != "" {
			result.Error = trimOutput(result.Error+": "+firstOutputLine(result.Output), 500)
		}
		return finish()
	}
	result.Status = "completed"
	return finish()
}

func runtimePromptArgs(id, prompt string) []string {
	// These are non-interactive modes supported by the corresponding CLIs.
	// Keep the mapping explicit so user input can never become a shell command.
	switch id {
	case "claude":
		return []string{"-p", prompt}
	case "codex":
		return []string{"exec", "--skip-git-repo-check", prompt}
	case "grok":
		return []string{"-p", prompt}
	case "kiro":
		return []string{"chat", "--no-interactive", prompt}
	default:
		return []string{prompt}
	}
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
