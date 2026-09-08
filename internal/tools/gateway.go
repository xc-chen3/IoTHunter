// Package tools provides the software execution boundary. A Capability never
// receives an arbitrary shell string; it submits an allow-listed tool name and
// argument vector to this gateway.
package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type PermissionSet struct {
	Network     bool
	Filesystem  string
	Destructive bool
}

type Definition struct {
	Name        string
	Path        string
	Isolation   string
	Image       string
	Permissions PermissionSet
	Timeout     time.Duration
	MaxOutput   int
}

type Request struct {
	Tool          string
	Args          []string
	WorkingDir    string
	WorkspaceRoot string
	Permissions   PermissionSet
	Timeout       time.Duration
}

type Result struct {
	Tool      string        `json:"tool"`
	Status    string        `json:"status"`
	Output    string        `json:"output,omitempty"`
	ExitCode  int           `json:"exit_code"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
}

type Gateway struct {
	mu    sync.RWMutex
	tools map[string]Definition
}

func NewGateway() *Gateway { return &Gateway{tools: map[string]Definition{}} }

func (g *Gateway) Register(definition Definition) error {
	definition.Name = strings.TrimSpace(definition.Name)
	definition.Path = strings.TrimSpace(definition.Path)
	if definition.Name == "" || definition.Path == "" {
		return errors.New("tool name and path are required")
	}
	definition.Isolation = strings.ToLower(strings.TrimSpace(definition.Isolation))
	if definition.Isolation == "" {
		definition.Isolation = "host"
	}
	if definition.Isolation != "host" && definition.Isolation != "docker" && definition.Isolation != "podman" {
		return fmt.Errorf("unsupported tool isolation %q", definition.Isolation)
	}
	if definition.Isolation != "host" && strings.TrimSpace(definition.Image) == "" {
		return errors.New("containerized tools require an image")
	}
	if definition.Timeout <= 0 {
		definition.Timeout = 5 * time.Minute
	}
	if definition.MaxOutput <= 0 || definition.MaxOutput > 64<<20 {
		definition.MaxOutput = 8 << 20
	}
	if definition.Isolation == "host" {
		if _, err := os.Stat(definition.Path); err != nil {
			return fmt.Errorf("tool %s is unavailable: %w", definition.Name, err)
		}
	} else if _, err := exec.LookPath(definition.Isolation); err != nil {
		return fmt.Errorf("tool isolation runtime %s is unavailable: %w", definition.Isolation, err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tools[definition.Name] = definition
	return nil
}

func (g *Gateway) List() []Definition {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Definition, 0, len(g.tools))
	for _, definition := range g.tools {
		out = append(out, definition)
	}
	return out
}

func (g *Gateway) Has(name string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.tools[name]
	return ok
}

func (g *Gateway) Run(parent context.Context, request Request) (Result, error) {
	started := time.Now()
	result := Result{Tool: request.Tool, Status: "failed", ExitCode: -1, StartedAt: started}
	g.mu.RLock()
	definition, ok := g.tools[request.Tool]
	g.mu.RUnlock()
	if !ok {
		result.Error = "tool is not registered"
		result.Duration = time.Since(started)
		return result, errors.New(result.Error)
	}
	if err := authorize(request.Permissions, definition.Permissions); err != nil {
		result.Error = err.Error()
		result.Duration = time.Since(started)
		return result, err
	}
	if request.WorkingDir == "" {
		request.WorkingDir = "."
	}
	if request.WorkspaceRoot != "" {
		if err := ensureInside(request.WorkspaceRoot, request.WorkingDir); err != nil {
			result.Error = err.Error()
			result.Duration = time.Since(started)
			return result, err
		}
	}
	timeout := definition.Timeout
	if request.Timeout > 0 && request.Timeout < timeout {
		timeout = request.Timeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command, args, dir := buildCommand(definition, request)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	cmd.Env = append([]string(nil), os.Environ()...)
	output, err := cmd.CombinedOutput()
	result.Output = trimOutput(output, definition.MaxOutput)
	result.Duration = time.Since(started)
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
		return result, ctx.Err()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		}
		result.Error = err.Error()
		return result, err
	}
	result.Status = "completed"
	result.ExitCode = 0
	return result, nil
}

func buildCommand(definition Definition, request Request) (string, []string, string) {
	if definition.Isolation == "host" {
		return definition.Path, append([]string(nil), request.Args...), request.WorkingDir
	}
	// Mount only the requested workspace directory. The tool still receives an
	// argument vector, and the container has no network unless the registered
	// definition and request both grant it.
	mountMode := "ro"
	if request.Permissions.Filesystem == "workspace-write" {
		mountMode = "rw"
	}
	args := []string{"run", "--rm", "--init", "--network", "none"}
	if request.Permissions.Network {
		args[len(args)-1] = "bridge"
	}
	args = append(args, "-v", request.WorkingDir+":/workspace:"+mountMode, "-w", "/workspace")
	if request.Permissions.Filesystem == "workspace-readonly" {
		args = append(args, "--read-only")
	}
	args = append(args, definition.Image, definition.Path)
	args = append(args, request.Args...)
	return definition.Isolation, args, request.WorkingDir
}

func ensureInside(root, path string) error {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	relative, err := filepath.Rel(filepath.Clean(resolvedRoot), filepath.Clean(resolvedPath))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("tool working directory is outside workspace root")
	}
	return nil
}

func authorize(request, definition PermissionSet) error {
	if request.Network && !definition.Network {
		return errors.New("tool does not allow network access")
	}
	if request.Destructive && !definition.Destructive {
		return errors.New("tool does not allow destructive access")
	}
	if request.Filesystem != "" && request.Filesystem != "workspace-readonly" && request.Filesystem != "workspace-write" {
		return errors.New("filesystem permission must be workspace-readonly or workspace-write")
	}
	if request.Filesystem == "workspace-write" && definition.Filesystem != "workspace-write" {
		return errors.New("tool does not allow workspace writes")
	}
	if definition.Filesystem == "" && request.Filesystem != "" {
		return errors.New("tool does not declare filesystem permissions")
	}
	return nil
}

func trimOutput(value []byte, max int) string {
	if len(value) > max {
		value = append(value[:max], []byte("\n...[output truncated]")...)
	}
	return strings.TrimSpace(string(value))
}
