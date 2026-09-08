// Package worker contains the process boundary used by heavy capabilities.
// Workers use one JSON request and one JSON result per invocation. Keeping the
// protocol line based makes it usable from Python, Go, or a container without
// coupling the control plane to a language runtime.
package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Request struct {
	RequestID    string         `json:"request_id"`
	TaskID       string         `json:"task_id"`
	AgentID      string         `json:"agent_id"`
	CapabilityID string         `json:"capability_id"`
	Objective    string         `json:"objective"`
	Inputs       map[string]any `json:"inputs,omitempty"`
	Permissions  map[string]any `json:"permissions,omitempty"`
	Budget       map[string]any `json:"budget,omitempty"`
}

type Result struct {
	RequestID    string           `json:"request_id"`
	CapabilityID string           `json:"capability_id"`
	Status       string           `json:"status"`
	Summary      string           `json:"summary"`
	Evidence     []map[string]any `json:"evidence,omitempty"`
	Artifacts    []map[string]any `json:"artifacts,omitempty"`
	Confidence   float64          `json:"confidence"`
	Metrics      map[string]any   `json:"metrics,omitempty"`
	Error        string           `json:"error,omitempty"`
}

type Spec struct {
	Python  string
	Command string
	Args    []string
	Dir     string
	Timeout time.Duration
}

type Runner struct {
	spec Spec
}

func NewRunner(spec Spec) Runner {
	if spec.Timeout <= 0 {
		spec.Timeout = 5 * time.Minute
	}
	return Runner{spec: spec}
}

func (r Runner) Run(parent context.Context, request Request) (Result, error) {
	if strings.TrimSpace(r.spec.Command) == "" {
		return Result{}, errors.New("worker command is not configured")
	}
	ctx := parent
	cancel := func() {}
	if r.spec.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, r.spec.Timeout)
	}
	defer cancel()

	args := append([]string(nil), r.spec.Args...)
	cmd := exec.CommandContext(ctx, r.spec.Command, args...)
	cmd.Dir = r.spec.Dir
	cmd.Env = append([]string(nil), os.Environ()...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, fmt.Errorf("worker stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("worker stdout: %w", err)
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start worker: %w", err)
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Result{}, fmt.Errorf("encode worker request: %w", err)
	}
	if _, err := stdin.Write(append(encoded, '\n')); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Result{}, fmt.Errorf("write worker request: %w", err)
	}
	_ = stdin.Close()

	line, readErr := readResultLine(stdout)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if readErr != nil {
		if waitErr != nil && stderr.String() != "" {
			return Result{}, fmt.Errorf("worker output: %w: %s", readErr, stderr.String())
		}
		return Result{}, fmt.Errorf("worker output: %w", readErr)
	}
	if waitErr != nil {
		return Result{}, fmt.Errorf("worker exited: %w: %s", waitErr, stderr.String())
	}
	var result Result
	if err := json.Unmarshal(line, &result); err != nil {
		return Result{}, fmt.Errorf("decode worker result: %w", err)
	}
	if result.Status == "" {
		return Result{}, errors.New("worker result has no status")
	}
	return result, nil
}

func readResultLine(reader io.Reader) ([]byte, error) {
	line, err := bufio.NewReaderSize(reader, 64*1024).ReadBytes('\n')
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		if err == nil {
			return nil, errors.New("worker returned an empty result")
		}
		return nil, err
	}
	if len(line) > 8<<20 {
		return nil, errors.New("worker result exceeds 8 MiB limit")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return line, nil
}

type limitedBuffer struct {
	data []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	const max = 16 << 10
	if len(b.data) < max {
		remaining := max - len(b.data)
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return written, nil
}

func (b *limitedBuffer) String() string { return strings.TrimSpace(string(b.data)) }

// KnowledgeSpec returns the bundled reference worker location. Deployments
// can override both values through IOTHUNTER_PYTHON and IOTHUNTER_WORKER_ROOT.
func KnowledgeSpec() Spec {
	python := os.Getenv("IOTHUNTER_PYTHON")
	if python == "" {
		python = "python3"
	}
	root := os.Getenv("IOTHUNTER_WORKER_ROOT")
	if root == "" {
		root = findProjectRoot()
	}
	script := filepath.Join(root, "capability-workers", "knowledge", "worker.py")
	return Spec{Python: python, Command: python, Args: []string{script}, Dir: root, Timeout: 5 * time.Minute}
}

func findProjectRoot() string {
	var starts []string
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if executable, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(executable))
	}
	for _, start := range starts {
		current := start
		for i := 0; i < 8; i++ {
			candidate := filepath.Join(current, "capability-workers", "knowledge", "worker.py")
			if _, err := os.Stat(candidate); err == nil {
				return current
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}
	if len(starts) > 0 {
		return starts[0]
	}
	return "."
}
