package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	args := runtimePromptArgs(id, prompt)
	var lastMessagePath string
	if id == "codex" {
		file, err := os.CreateTemp("", "iothunter-codex-last-*.txt")
		if err != nil {
			result.Error = fmt.Sprintf("prepare Codex output: %v", err)
			return finish()
		}
		lastMessagePath = file.Name()
		_ = file.Close()
		defer os.Remove(lastMessagePath)
		args = runtimePromptArgsWithOutput(id, prompt, lastMessagePath)
	}
	cmd := exec.CommandContext(runCtx, path, args...)
	cmd.Env = os.Environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	result.Output = trimOutput(sanitizeRuntimeOutput(string(output)), 64<<10)
	if lastMessagePath != "" {
		if final, readErr := os.ReadFile(lastMessagePath); readErr == nil && strings.TrimSpace(string(final)) != "" {
			result.Output = trimOutput(sanitizeRuntimeOutput(string(final)), 64<<10)
		}
	}
	if runCtx.Err() != nil {
		result.Error = runCtx.Err().Error()
		return finish()
	}
	if err != nil {
		result.Error = safeRuntimeError(err)
		detail := firstOutputLine(stderr.String())
		if detail == "" {
			detail = firstOutputLine(result.Output)
		}
		if detail != "" {
			result.Error = trimOutput(result.Error+": "+detail, 500)
		}
		return finish()
	}
	if result.Output == "" {
		result.Error = "runtime returned an empty response"
		return finish()
	}
	if id == "kiro" {
		lower := strings.ToLower(result.Output)
		for _, marker := range []string{"rate limit reached", "quota exceeded", "authentication required", "not logged in", "unauthorized"} {
			if strings.Contains(lower, marker) {
				result.Error = trimOutput(result.Output, 500)
				return finish()
			}
		}
	}
	result.Status = "completed"
	return finish()
}

func runtimePromptArgs(id, prompt string) []string {
	return runtimePromptArgsWithOutput(id, prompt, "")
}

func runtimePromptArgsWithOutput(id, prompt, outputPath string) []string {
	// These are non-interactive modes supported by the corresponding CLIs.
	// Keep the mapping explicit so user input can never become a shell command.
	switch id {
	case "claude":
		return []string{"-p", "--tools", "", "--permission-mode", "dontAsk", "--no-session-persistence", prompt}
	case "codex":
		args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "--color", "never", "--sandbox", "read-only"}
		if outputPath != "" {
			args = append(args, "--output-last-message", outputPath)
		}
		return append(args, prompt)
	case "grok":
		return []string{"-p", prompt, "--format", "text", "--sandbox"}
	case "kiro":
		return []string{"chat", "--no-interactive", "--wrap", "never", prompt}
	default:
		return []string{prompt}
	}
}

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func sanitizeRuntimeOutput(value string) string {
	value = ansiEscapePattern.ReplaceAllString(value, "")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

// ResolveConversationRuntime selects an explicit runtime first, then the
// Commander's configured runtime, and finally the first installed runtime.
// The returned executable has already been checked for local availability.
func (e *Engine) ResolveConversationRuntime(explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		spec, ok := localRuntimeSpec(explicit)
		if !ok {
			return "", fmt.Errorf("unknown runtime %q", explicit)
		}
		if runtimePath(spec) == "" {
			return "", fmt.Errorf("%s is not installed", spec.name)
		}
		return explicit, nil
	}
	for _, agent := range e.Store.Snapshot().Agents {
		if agent.ID != "commander-default" || strings.TrimSpace(agent.RuntimeID) == "" {
			continue
		}
		spec, ok := localRuntimeSpec(agent.RuntimeID)
		if ok && runtimePath(spec) != "" {
			return agent.RuntimeID, nil
		}
	}
	for _, spec := range localRuntimeSpecs {
		if runtimePath(spec) != "" {
			return spec.id, nil
		}
	}
	return "", fmt.Errorf("no local AI runtime is installed or configured")
}

// BuildConversationPrompt gives the local runtime bounded, structured context
// without granting it direct access to tasks, tools, or peripherals.
func (e *Engine) BuildConversationPrompt(conversation Conversation, content, targetID string) string {
	state := e.Store.Snapshot()
	workspace, _ := e.Store.Workspace(conversation.WorkspaceID)
	var builder strings.Builder
	builder.WriteString("You are Commander in IoTHunter, a local IoT security research client.\n")
	builder.WriteString("Answer the user's question directly and accurately. Separate facts, hypotheses, and actions that would require an IoTHunter task. Do not claim that tools, capabilities, devices, or network operations ran unless the conversation explicitly contains their recorded result.\n\n")
	fmt.Fprintf(&builder, "Workspace: %s\n", workspace.Name)
	if workspace.Description != "" {
		fmt.Fprintf(&builder, "Workspace description: %s\n", workspace.Description)
	}
	if targetID != "" {
		if target, ok := findTarget(state.Targets, targetID); ok && target.WorkspaceID == conversation.WorkspaceID {
			fmt.Fprintf(&builder, "Selected IoT target: %s; vendor=%s; model=%s; transport=%s; address=%s; authorized=%t\n", target.Name, target.Vendor, target.Model, target.Transport, target.Address, target.Authorized)
		}
	}
	builder.WriteString("\nRecent conversation:\n")
	messages := conversation.Messages
	if len(messages) > 12 {
		messages = messages[len(messages)-12:]
	}
	for _, message := range messages {
		role := strings.ToUpper(strings.TrimSpace(message.Role))
		if role == "" {
			role = "USER"
		}
		value := strings.TrimSpace(message.Content)
		if len(value) > 4000 {
			value = value[:4000] + "..."
		}
		fmt.Fprintf(&builder, "%s: %s\n", role, value)
	}
	fmt.Fprintf(&builder, "USER: %s\nASSISTANT:", strings.TrimSpace(content))
	return trimOutput(builder.String(), 32<<10)
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
