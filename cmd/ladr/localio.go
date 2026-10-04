// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ladr/ladr/internal/device"
	"github.com/ladr/ladr/internal/engine"
	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/provider"
	"github.com/ladr/ladr/internal/relik"
)

// localIO is the standalone EngineIO: in-memory step outputs, stderr events,
// real LLM calls through the provider adapters, local interpreter sessions,
// workspace-scoped files: ops, in-process recursion. No Redis, no Postgres.
type localIO struct {
	mu        sync.Mutex
	outputs   map[string]string
	events    []string
	nodeCount map[string]int

	workspace string
	sessions  *device.SessionRunner
	verbose   bool
	instance  string
	cfg       *ladr.PipelineConfig
}

func outKey(instanceID string, loop int, stepID, groupID string, node *int) string {
	n := 0
	if node != nil {
		n = *node
	}
	g := groupID
	if g == "" {
		g = "_"
	}
	return fmt.Sprintf("%s/L%d/%s/%s/N%d", instanceID, loop, stepID, g, n)
}

func (io *localIO) ReadOutput(_ context.Context, a engine.ReadOutputArgs) (string, error) {
	io.mu.Lock()
	defer io.mu.Unlock()
	return io.outputs[outKey(a.InstanceID, a.LoopIndex, a.StepID, a.GroupID, a.NodeNumber)], nil
}

func (io *localIO) ReadOutputArray(_ context.Context, a engine.ReadOutputArrayArgs) ([]string, error) {
	io.mu.Lock()
	defer io.mu.Unlock()
	out := make([]string, 0, len(a.FromSources))
	for _, src := range a.FromSources {
		out = append(out, io.outputs[outKey(a.InstanceID, src.LoopIndex, src.StepID, src.GroupID, src.NodeNumber)])
	}
	return out, nil
}

func (io *localIO) WriteOutput(_ context.Context, a engine.WriteOutputArgs) error {
	io.mu.Lock()
	defer io.mu.Unlock()
	io.outputs[outKey(a.InstanceID, a.LoopIndex, a.StepID, a.GroupID, a.NodeNumber)] = a.Output
	return nil
}

func (io *localIO) PublishEvent(_ context.Context, a engine.PublishEventArgs) error {
	io.mu.Lock()
	line := fmt.Sprintf("[%s] %s", a.InstanceID, a.Type)
	io.events = append(io.events, line)
	io.mu.Unlock()
	if io.verbose {
		if len(a.Data) > 0 {
			fmt.Fprintf(os.Stderr, "· %s %v\n", line, a.Data)
		} else {
			fmt.Fprintln(os.Stderr, "·", line)
		}
	}
	return nil
}

func (io *localIO) finalTrail() string {
	io.mu.Lock()
	defer io.mu.Unlock()
	return strings.Join(io.events, "\n")
}

// ExecuteLlmCall runs the provider chain synchronously, first success wins —
// same policy the worker applies over the queue.
func (io *localIO) ExecuteLlmCall(ctx context.Context, a engine.ExecuteLlmParams) (string, error) {
	attempts := append([]engine.ProviderConfig{a.Provider}, a.FallbackProviders...)
	messages := []provider.Message{{Role: provider.RoleUser, ContentStr: a.UserPrompt}}

	var lastErr error
	for _, attempt := range attempts {
		adapter, err := provider.GetAdapter(attempt.Provider)
		if err != nil {
			lastErr = fmt.Errorf("provider %s: %w", attempt.Provider, err)
			continue
		}
		cfg := toProviderConfig(attempt)
		result, err := adapter.Call(ctx, cfg.Model, messages, cfg)
		if err != nil {
			lastErr = fmt.Errorf("provider %s (%s): %w", attempt.Provider, attempt.Model, err)
			continue
		}
		if a.UsageOut != nil && result != nil && result.Usage != nil {
			a.UsageOut.PromptTokens = result.Usage.PromptTokens
			a.UsageOut.CompletionTokens = result.Usage.CompletionTokens
		}
		return result.Text, nil
	}
	if lastErr != nil {
		fmt.Fprintf(os.Stderr, "! llm call failed: %v\n", lastErr)
	}
	return "", lastErr
}

func toProviderConfig(c engine.ProviderConfig) provider.ProviderConfig {
	pc := provider.ProviderConfig{
		Provider: c.Provider,
		Model:    c.Model,
		APIKey:   c.APIKey,
		BaseURL:  c.BaseURL,
		TimeoutMs: c.TimeoutMs,
	}
	if c.Temperature != 0 {
		t := c.Temperature
		pc.Temperature = &t
	}
	if c.MaxTokens != 0 {
		m := c.MaxTokens
		pc.MaxTokens = &m
	}
	return pc
}

// ExecuteCode runs LLM-emitted code in a persistent local interpreter session
// (python/node/bash) under the sessions dir.
func (io *localIO) ExecuteCode(ctx context.Context, a engine.ExecuteCodeParams) (engine.ExecuteCodeResult, error) {
	sid := fmt.Sprintf("%s-%s-%s-L%d-N%d-D%d",
		a.InstanceID, a.StepID, nonEmpty(a.GroupID, "_"), a.LoopIndex, intPtrOr(a.NodeNumber, 0), a.Depth)
	res, err := io.sessions.Exec(ctx, device.ExecParams{
		SessionID: sid,
		Runtime:   device.Runtime(a.Runtime),
		Code:      a.Code,
		TimeoutMs: a.TimeoutMs,
	})
	if err != nil {
		return engine.ExecuteCodeResult{}, err
	}
	return engine.ExecuteCodeResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Output:   res.Stdout,
		Truncated: res.Truncated,
	}, nil
}

// FilesOp scopes files: read/write/list to the run's workspace directory.
func (io *localIO) FilesOp(_ context.Context, a engine.FilesOpParams) (engine.FilesOpResult, error) {
	root, err := filepath.Abs(io.workspace)
	if err != nil {
		return engine.FilesOpResult{}, err
	}
	p := filepath.Join(root, a.Path)
	if !strings.HasPrefix(p, root+string(os.PathSeparator)) && p != root {
		return engine.FilesOpResult{}, fmt.Errorf("files: %s escapes the run workspace", a.Path)
	}
	switch a.Op {
	case "read":
		b, err := os.ReadFile(p)
		if err != nil {
			return engine.FilesOpResult{}, fmt.Errorf("files: read %s: %w", a.Path, err)
		}
		const max = 64 * 1024
		trunc := len(b) > max
		if trunc {
			b = b[:max]
		}
		return engine.FilesOpResult{Content: string(b), Truncated: trunc}, nil
	case "write":
		if a.Append {
			f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return engine.FilesOpResult{}, fmt.Errorf("files: write %s: %w", a.Path, err)
			}
			defer f.Close()
			if _, err := f.WriteString(a.Content); err != nil {
				return engine.FilesOpResult{}, err
			}
			return engine.FilesOpResult{Bytes: len(a.Content)}, nil
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return engine.FilesOpResult{}, err
		}
		if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
			return engine.FilesOpResult{}, fmt.Errorf("files: write %s: %w", a.Path, err)
		}
		return engine.FilesOpResult{Bytes: len(a.Content)}, nil
	case "list":
		entries, err := os.ReadDir(p)
		if err != nil {
			return engine.FilesOpResult{}, fmt.Errorf("files: list %s: %w", a.Path, err)
		}
		var names []string
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		return engine.FilesOpResult{Entries: names}, nil
	}
	return engine.FilesOpResult{}, fmt.Errorf("files: unknown op %q", a.Op)
}

// Recurse executes the child config in-process with its own localIO and
// returns its exit output.
func (io *localIO) Recurse(ctx context.Context, a engine.RecurseArgs) (string, error) {
	child := &localIO{
		outputs:   map[string]string{},
		workspace: io.workspace,
		sessions:  io.sessions,
		verbose:   io.verbose,
		instance:  fmt.Sprintf("%s-r%d", a.ParentInstanceID, a.Depth),
		cfg:       a.Config,
	}
	store := engine.NewWriteExecutor(relik.NewMemoryStore())
	return engine.ExecutePipeline(ctx, engine.ExecutePipelineOptions{
		Config: a.Config,
		Context: engine.ExecutionRuntimeContext{
			InstanceID: child.instance,
			Knobs:      a.Knobs,
			Inputs:     map[string]any{"context": a.Context},
		},
		IO:            child,
		Provider:      a.Provider,
		MaxLoops:      3,
		WriteExecutor: store,
	})
}

// Handoff to a different ladder needs a ladder registry; standalone runs a
// single file, so only self-handoff is resolvable.
func (io *localIO) Handoff(_ context.Context, a engine.HandoffArgs) (engine.HandoffResult, error) {
	return engine.HandoffResult{}, fmt.Errorf("handoff to %q is not supported in standalone mode (single-file run)", a.Target.LadderID)
}

func (io *localIO) SetPauseState(context.Context, engine.PausedPosition) error { return nil }
func (io *localIO) ShouldPause(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (io *localIO) GetSystemPromptOverride(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (io *localIO) GetNodeCount(_ context.Context, a engine.GetNodeCountArgs) (int, error) {
	io.mu.Lock()
	defer io.mu.Unlock()
	return io.nodeCount[fmt.Sprintf("%s/%s/%s/%d", a.InstanceID, a.StepID, a.GroupID, a.LoopIndex)], nil
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func intPtrOr(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}
