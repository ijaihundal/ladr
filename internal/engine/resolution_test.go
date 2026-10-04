// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// mockEngineIO is a test double for EngineIO, backed by an in-memory map.
type mockEngineIO struct {
	mu         sync.Mutex
	outputs    map[string]string
	nodeCounts map[string]int
	writes     map[string]string
	events     []string
	llmFn      func(args ExecuteLlmParams) string
	recurseFn  func(args RecurseArgs) string
	handoffFn  func(args HandoffArgs) (HandoffResult, error)
}

func (m *mockEngineIO) outKey(loop int, step string, node *int) string {
	n := 0
	if node != nil {
		n = *node
	}
	return fmt.Sprintf("%d:%s:%d", loop, step, n)
}

func (m *mockEngineIO) nodeKey(loop int, step string) string {
	return fmt.Sprintf("%d:%s", loop, step)
}

func (m *mockEngineIO) ReadOutput(_ context.Context, a ReadOutputArgs) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outputs == nil {
		return "", nil
	}
	return m.outputs[m.outKey(a.LoopIndex, a.StepID, a.NodeNumber)], nil
}

func (m *mockEngineIO) ReadOutputArray(_ context.Context, a ReadOutputArrayArgs) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, s := range a.FromSources {
		if v, ok := m.outputs[m.outKey(s.LoopIndex, s.StepID, s.NodeNumber)]; ok && v != "" {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *mockEngineIO) WriteOutput(_ context.Context, a WriteOutputArgs) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writes == nil {
		m.writes = make(map[string]string)
	}
	key := m.outKey(a.LoopIndex, a.StepID, a.NodeNumber)
	m.writes[key] = a.Output
	if m.outputs == nil {
		m.outputs = make(map[string]string)
	}
	m.outputs[key] = a.Output
	return nil
}

func (m *mockEngineIO) PublishEvent(_ context.Context, a PublishEventArgs) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, string(a.Type))
	return nil
}

func (m *mockEngineIO) ExecuteLlmCall(_ context.Context, args ExecuteLlmParams) (string, error) {
	if m.llmFn != nil {
		return m.llmFn(args), nil
	}
	return "", nil
}
func (m *mockEngineIO) Recurse(_ context.Context, args RecurseArgs) (string, error) {
	if m.recurseFn != nil {
		return m.recurseFn(args), nil
	}
	return "", nil
}
func (m *mockEngineIO) Handoff(_ context.Context, args HandoffArgs) (HandoffResult, error) {
	if m.handoffFn != nil {
		return m.handoffFn(args)
	}
	return HandoffResult{}, fmt.Errorf("handoff: no handoffFn wired in mockEngineIO")
}
func (m *mockEngineIO) SetPauseState(context.Context, PausedPosition) error { return nil }
func (m *mockEngineIO) ShouldPause(context.Context, string) (bool, error)   { return false, nil }
func (m *mockEngineIO) GetSystemPromptOverride(context.Context, string) (string, error) {
	return "", nil
}
func (m *mockEngineIO) ExecuteCode(context.Context, ExecuteCodeParams) (ExecuteCodeResult, error) {
	return ExecuteCodeResult{Output: "(mock: code execution not available)"}, nil
}
func (m *mockEngineIO) FilesOp(context.Context, FilesOpParams) (FilesOpResult, error) {
	return FilesOpResult{}, fmt.Errorf("mock: files not available")
}
func (m *mockEngineIO) GetNodeCount(_ context.Context, a GetNodeCountArgs) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nodeCounts == nil {
		return 0, nil
	}
	return m.nodeCounts[m.nodeKey(a.LoopIndex, a.StepID)], nil
}

// Ported from tests/unit/resolve-node-count.test.ts.
func baseRuntime() *ExecutionRuntimeContext {
	node := 2
	return &ExecutionRuntimeContext{
		InstanceID:        "instance",
		LoopIndex:         1,
		Depth:             0,
		CurrentStepID:     "current-step",
		CurrentNodeNumber: &node,
		Knobs:             map[string]float64{"fanout": 4},
	}
}

func TestResolveNodeCount_NumericAndKnobReference(t *testing.T) {
	ctx := context.Background()
	io := &mockEngineIO{}

	got, err := ResolveNodeCount(ctx, &ladr.Nodes{Kind: ladr.NodesNumber, Num: 3}, baseRuntime(), io)
	if err != nil {
		t.Fatalf("numeric: %v", err)
	}
	if got != 3 {
		t.Errorf("numeric: got %d, want 3", got)
	}

	got, err = ResolveNodeCount(ctx, &ladr.Nodes{Kind: ladr.NodesString, Str: "{{knobs.fanout}}"}, baseRuntime(), io)
	if err != nil {
		t.Fatalf("knob ref: %v", err)
	}
	if got != 4 {
		t.Errorf("knob ref: got %d, want 4", got)
	}
}

func TestResolveNodeCount_StepOutputReference(t *testing.T) {
	ctx := context.Background()
	io := &mockEngineIO{outputs: map[string]string{"1:planner:0": "5"}}

	got, err := ResolveNodeCount(ctx, &ladr.Nodes{Kind: ladr.NodesString, Str: "{{planner.output}}"}, baseRuntime(), io)
	if err != nil {
		t.Fatalf("step output: %v", err)
	}
	if got != 5 {
		t.Errorf("step output: got %d, want 5", got)
	}
}

func TestResolveNodeCount_PrunedNodesFrom(t *testing.T) {
	ctx := context.Background()
	io := &mockEngineIO{outputs: map[string]string{
		"1:evaluate:1": "7",
		"1:evaluate:3": "9",
	}}

	rt := baseRuntime()
	rt.PrunedNodeMap = NewPrunedNodeMap()
	rt.PrunedNodeMap.Set(GetPrunedNodeMapKey(1, "evaluate"), []int{1, 3})

	nodes := &ladr.Nodes{
		Kind: ladr.NodesFromObj,
		From: ladr.NodesFrom{
			StepID:  "evaluate",
			LoopRef: ladr.LoopRef{Kind: ladr.LoopRefString, Str: "current"},
			Pruned:  true,
		},
	}

	got, err := ResolveNodeCount(ctx, nodes, rt, io)
	if err != nil {
		t.Fatalf("pruned: %v", err)
	}
	if got != 2 {
		t.Errorf("pruned: got %d, want 2", got)
	}
}
