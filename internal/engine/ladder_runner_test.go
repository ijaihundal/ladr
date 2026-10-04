// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version of the License, or (at your
// option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.

package engine

// ladder_runner_test.go — shared infrastructure for running real ladder configs
// end-to-end through ExecutePipeline. Provides:
//   - ladderIO: a combined EngineIO double that scripts LLM output sequences,
//     records code-execution calls, persists step outputs (so ingest fields
//     work), and captures events.
//   - runLadder: the one-call helper to parse a JSON ladder config, wire up a
//     MemoryStore seeded from the ladder's reliks, execute, and return the
//     result + IO double + store for assertions.
//
// This fills the gap between the existing test doubles:
//   - mockEngineIO persists outputs but its LLM is callback-based (hard to
//     script multi-turn tool loops where the sequence matters).
//   - scriptIO scripts LLM sequences and records code calls but its
//     WriteOutput/ReadOutput are no-ops (so ingest fields across steps break).
// ladderIO does both: scripted LLM + real output persistence + code recording.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

// ladderIO is the combined test double for ladder-level integration tests.
type ladderIO struct {
	mu sync.Mutex

	// LLM scripting: outputs consumed in order, one per ExecuteLlmCall.
	llmOutputs []string
	llmIdx     int
	prompts    []string // captured UserPrompt values for assertions

	// Code execution recording.
	codeCalls  []ExecuteCodeParams
	codeResult ExecuteCodeResult
	codeErr    error
	codeFn     func(a ExecuteCodeParams) (ExecuteCodeResult, error) // per-call override (terminal scripting)

	// Files-op recording.
	filesCalls  []FilesOpParams
	filesResult FilesOpResult
	filesErr    error

	// Output persistence (so ingest/readOutput across steps work).
	outputs map[string]string // key = outKey(loop,step,node)
	writes  map[string]string // captured writes for assertions

	// Events.
	events []string

	// Recursion + handoff (callback-based, like mockEngineIO).
	recurseFn func(args RecurseArgs) string
	handoffFn func(args HandoffArgs) (HandoffResult, error)

	// Node counts (for nodes.from resolution).
	nodeCounts map[string]int
}

func newLadderIO(llmOutputs []string) *ladderIO {
	return &ladderIO{
		llmOutputs: llmOutputs,
		outputs:    make(map[string]string),
		writes:     make(map[string]string),
	}
}

func (l *ladderIO) outKey(loop int, step string, node *int) string {
	n := 0
	if node != nil {
		n = *node
	}
	return fmt.Sprintf("%d:%s:%d", loop, step, n)
}

func (l *ladderIO) nodeKey(loop int, step string) string {
	return fmt.Sprintf("%d:%s", loop, step)
}

func (l *ladderIO) ExecuteLlmCall(_ context.Context, a ExecuteLlmParams) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prompts = append(l.prompts, a.UserPrompt)
	if l.llmIdx >= len(l.llmOutputs) {
		return "(no more scripted LLM output)", nil
	}
	out := l.llmOutputs[l.llmIdx]
	l.llmIdx++
	return out, nil
}

func (l *ladderIO) ExecuteCode(_ context.Context, a ExecuteCodeParams) (ExecuteCodeResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.codeCalls = append(l.codeCalls, a)
	if l.codeFn != nil {
		return l.codeFn(a)
	}
	if l.codeErr != nil {
		return ExecuteCodeResult{}, l.codeErr
	}
	return l.codeResult, nil
}

func (l *ladderIO) FilesOp(_ context.Context, a FilesOpParams) (FilesOpResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.filesCalls = append(l.filesCalls, a)
	if l.filesErr != nil {
		return FilesOpResult{}, l.filesErr
	}
	return l.filesResult, nil
}

func (l *ladderIO) ReadOutput(_ context.Context, a ReadOutputArgs) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.outputs[l.outKey(a.LoopIndex, a.StepID, a.NodeNumber)], nil
}

func (l *ladderIO) ReadOutputArray(_ context.Context, a ReadOutputArrayArgs) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, s := range a.FromSources {
		if v, ok := l.outputs[l.outKey(s.LoopIndex, s.StepID, s.NodeNumber)]; ok && v != "" {
			out = append(out, v)
		}
	}
	return out, nil
}

func (l *ladderIO) WriteOutput(_ context.Context, a WriteOutputArgs) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := l.outKey(a.LoopIndex, a.StepID, a.NodeNumber)
	l.writes[key] = a.Output
	l.outputs[key] = a.Output
	return nil
}

func (l *ladderIO) PublishEvent(_ context.Context, a PublishEventArgs) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, string(a.Type))
	return nil
}

func (l *ladderIO) Recurse(_ context.Context, args RecurseArgs) (string, error) {
	if l.recurseFn != nil {
		return l.recurseFn(args), nil
	}
	return "", nil
}

func (l *ladderIO) Handoff(_ context.Context, args HandoffArgs) (HandoffResult, error) {
	if l.handoffFn != nil {
		return l.handoffFn(args)
	}
	return HandoffResult{}, fmt.Errorf("handoff: no handoffFn wired in ladderIO")
}

func (l *ladderIO) SetPauseState(context.Context, PausedPosition) error { return nil }
func (l *ladderIO) ShouldPause(context.Context, string) (bool, error)   { return false, nil }
func (l *ladderIO) GetSystemPromptOverride(context.Context, string) (string, error) {
	return "", nil
}
func (l *ladderIO) GetNodeCount(_ context.Context, a GetNodeCountArgs) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nodeCounts == nil {
		return 0, nil
	}
	return l.nodeCounts[l.nodeKey(a.LoopIndex, a.StepID)], nil
}

// withCodeResult sets the fake code execution result and returns the IO for chaining.
func (l *ladderIO) withCodeResult(r ExecuteCodeResult) *ladderIO {
	l.codeResult = r
	return l
}

// withCodeErr sets a fake code execution error and returns the IO for chaining.
func (l *ladderIO) withCodeErr(err error) *ladderIO {
	l.codeErr = err
	return l
}

// withFilesResult sets the fake files-op result and returns the IO for chaining.
func (l *ladderIO) withFilesResult(r FilesOpResult) *ladderIO {
	l.filesResult = r
	return l
}

// withFilesErr sets a fake files-op error and returns the IO for chaining.
func (l *ladderIO) withFilesErr(err error) *ladderIO {
	l.filesErr = err
	return l
}

// withRecurseFn sets the recursion callback and returns the IO for chaining.
func (l *ladderIO) withRecurseFn(fn func(args RecurseArgs) string) *ladderIO {
	l.recurseFn = fn
	return l
}

// withHandoffFn sets the handoff callback and returns the IO for chaining.
func (l *ladderIO) withHandoffFn(fn func(args HandoffArgs) (HandoffResult, error)) *ladderIO {
	l.handoffFn = fn
	return l
}

// --- runLadder helpers ---

// ladderTestProvider is the default provider for ladder runner tests.
func ladderTestProvider() ProviderConfig {
	return ProviderConfig{
		Provider: "openrouter",
		Model:    "test/model",
		APIKey:   "test-key",
	}
}

// runLadder parses a JSON ladder config, creates a MemoryStore seeded from the
// config's declared reliks, executes the pipeline, and returns the exit output +
// the IO double + the store. The caller asserts on all three.
//
// The store is returned even on error — partial side effects may have occurred.
func runLadder(t *testing.T, cfgJSON string, io *ladderIO, maxLoops int) (string, error, *relik.MemoryStore) {
	t.Helper()
	cfg, err := ladr.ParseStoredConfig([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("ParseStoredConfig: %v", err)
	}
	store := relik.NewMemoryStore()
	result, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:    cfg,
		Context:   ExecutionRuntimeContext{InstanceID: "ladder-test", Inputs: map[string]any{}},
		IO:        io,
		Provider:  ladderTestProvider(),
		MaxLoops:  maxLoops,
		RelikStore: store,
	})
	return result, err, store
}

// runLadderWithInputs is like runLadder but allows seeding input context values.
func runLadderWithInputs(t *testing.T, cfgJSON string, io *ladderIO, maxLoops int, inputs map[string]any) (string, error, *relik.MemoryStore) {
	t.Helper()
	cfg, err := ladr.ParseStoredConfig([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("ParseStoredConfig: %v", err)
	}
	store := relik.NewMemoryStore()
	result, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:   cfg,
		Context:  ExecutionRuntimeContext{InstanceID: "ladder-test", Inputs: inputs},
		IO:       io,
		Provider: ladderTestProvider(),
		MaxLoops: maxLoops,
		RelikStore: store,
	})
	return result, err, store
}

// runLadderWithExistingStore runs a ladder using a pre-existing store (for
// multi-run persistence tests where the store must survive across runs).
func runLadderWithExistingStore(t *testing.T, cfgJSON string, io *ladderIO, maxLoops int, store *relik.MemoryStore) (string, error, *relik.MemoryStore) {
	t.Helper()
	cfg, err := ladr.ParseStoredConfig([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("ParseStoredConfig: %v", err)
	}
	result, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:    cfg,
		Context:   ExecutionRuntimeContext{InstanceID: "ladder-test", Inputs: map[string]any{}},
		IO:        io,
		Provider:  ladderTestProvider(),
		MaxLoops:  maxLoops,
		RelikStore: store,
	})
	return result, err, store
}

// mustReadRelik reads a relik file and fatals on error.
func mustReadRelik(t *testing.T, store *relik.MemoryStore, relikTitle, folder, file string) string {
	t.Helper()
	content, err := store.ReadFile(relikTitle, folder, file)
	if err != nil {
		t.Fatalf("ReadFile(%s/%s/%s): %v", relikTitle, folder, file, err)
	}
	return content
}

// mustReadMetatag reads a metatag and fatals on error.
func mustReadMetatag(t *testing.T, store *relik.MemoryStore, relikTitle, folder, file, tag string) any {
	t.Helper()
	val, err := store.ReadMetatag(relikTitle, folder, file, tag)
	if err != nil {
		t.Fatalf("ReadMetatag(%s/%s/%s/%s): %v", relikTitle, folder, file, tag, err)
	}
	return val
}

// assertContains checks that haystack contains needle.
func assertContains(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if !contains(haystack, needle) {
		t.Errorf("%s: expected %q to contain %q", label, haystack, needle)
	}
}
