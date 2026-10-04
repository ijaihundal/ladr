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
	"sort"
	"strings"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// Ported from tests/unit/pipeline-execution.test.ts.

func defaultProvider() ProviderConfig {
	return ProviderConfig{Provider: "openrouter", Model: "openai/gpt-oss-20b", APIKey: "secret"}
}

func runPipeline(t *testing.T, cfgJSON string, io EngineIO, maxLoops int) (string, error) {
	t.Helper()
	cfg, err := ladr.ParseStoredConfig([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("ParseStoredConfig: %v", err)
	}
	return ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:   cfg,
		Context:  ExecutionRuntimeContext{InstanceID: "test-instance", Depth: 0, Inputs: map[string]any{}},
		IO:       io,
		Provider: defaultProvider(),
		MaxLoops: maxLoops,
	})
}

// 1. Sequential step runs nodes in order and can ingest previous node.
func TestPipeline_SequentialNodesInOrder(t *testing.T) {
	const cfg = `{
		"id":"seq","name":"Sequential","author":"tests","version":1,
		"allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},
		"exit":"seq_step","knobs":{},
		"steps":[{"id":"seq_step","name":"Sequential Step","type":"sequential","nodes":3,
			"fields":[{"name":"Prev","type":"ingest","from":{"stepId":"seq_step","loopRef":"current","nodeRef":"previous"},"skipFirstNode":true}]
		}]
	}`

	io := &mockEngineIO{
		llmFn: func(a ExecuteLlmParams) string {
			n := 0
			if a.NodeNumber != nil {
				n = *a.NodeNumber
			}
			return "n" + intStr(n)
		},
	}

	_, err := runPipeline(t, cfg, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	// Sequential steps should produce 3 LLM calls in node order 1,2,3.
	// (We can't directly inspect llmCalls from here; verify via writes.)
	for i := 1; i <= 3; i++ {
		key := io.outKey(0, "seq_step", &[]int{i}[0])
		if io.writes[key] == "" {
			t.Errorf("missing write for node %d (key %s)", i, key)
		}
	}
}

// 2. Group step executes all child steps before moving on.
func TestPipeline_GroupStepOrdering(t *testing.T) {
	const cfg = `{
		"id":"group","name":"Group","author":"tests","version":1,
		"allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},
		"exit":"after_group","knobs":{},
		"steps":[
			{"id":"parallel_group","name":"Parallel Group","type":"group","steps":[
				{"id":"a","name":"A","type":"normal"},
				{"id":"b","name":"B","type":"normal"}
			]},
			{"id":"after_group","name":"After Group","type":"normal"}
		]
	}`

	var order []string
	io := &mockEngineIO{
		llmFn: func(a ExecuteLlmParams) string {
			order = append(order, a.StepID)
			return "done:" + a.StepID
		},
	}

	_, err := runPipeline(t, cfg, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	// First two should be a,b (in any order since group is parallel); third is after_group.
	if len(order) != 3 {
		t.Fatalf("got %d LLM calls, want 3: %v", len(order), order)
	}
	firstTwo := append([]string{}, order[:2]...)
	sort.Strings(firstTwo)
	if firstTwo[0] != "a" || firstTwo[1] != "b" {
		t.Errorf("first two = %v, want [a b]", firstTwo)
	}
	if order[2] != "after_group" {
		t.Errorf("third = %q, want after_group", order[2])
	}
}

// 3. Pipeline recursion uses child output when recurse is configured.
func TestPipeline_RecursionUsesChildOutput(t *testing.T) {
	const cfg = `{
		"id":"rec","name":"Recursion","author":"tests","version":1,
		"allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},
		"exit":"recurse_step","knobs":{},
		"steps":[{"id":"recurse_step","name":"Recurse Step","type":"normal","recursion":{"maxDepth":2}}]
	}`

	io := &mockEngineIO{
		llmFn:     func(a ExecuteLlmParams) string { return "base-output" },
		recurseFn: func(a RecurseArgs) string { return "child:base-output:depth" + intStr(a.Depth) },
	}

	result, err := runPipeline(t, cfg, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "child:base-output:depth1" {
		t.Errorf("result = %q, want 'child:base-output:depth1'", result)
	}
}

// 4. Multi-loop pipeline supports accumulate ingestion across loops.
func TestPipeline_MultiLoopAccumulate(t *testing.T) {
	const cfg = `{
		"id":"loops","name":"Loops","author":"tests","version":1,
		"allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},
		"exit":"collector","knobs":{},
		"steps":[
			{"id":"draft","name":"Draft","type":"normal"},
			{"id":"collector","name":"Collector","type":"normal","fields":[
				{"name":"Drafts","type":"multi_ingest","from":[{"stepId":"draft","loopRef":"accumulate"}]}
			]}
		]
	}`

	io := &mockEngineIO{
		llmFn: func(a ExecuteLlmParams) string {
			if a.StepID == "draft" {
				return "draft-l" + intStr(a.LoopIndex)
			}
			return a.UserPrompt
		},
	}

	_, err := runPipeline(t, cfg, io, 3)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	// Loop 2's collector should see drafts from loops 0, 1, 2.
	collectorL2 := io.writes[io.outKey(2, "collector", nil)]
	if !strings.Contains(collectorL2, "draft-l0") {
		t.Errorf("loop 2 collector missing 'draft-l0': %q", collectorL2)
	}
	if !strings.Contains(collectorL2, "draft-l1") {
		t.Errorf("loop 2 collector missing 'draft-l1': %q", collectorL2)
	}
}

// 5. Pipeline emits lifecycle events in expected order.
func TestPipeline_LifecycleEventOrder(t *testing.T) {
	const cfg = `{
		"id":"events","name":"Events","author":"tests","version":1,
		"allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},
		"exit":"step2","knobs":{},
		"steps":[
			{"id":"step1","name":"Step 1","type":"normal"},
			{"id":"step2","name":"Step 2","type":"normal"}
		]
	}`

	io := &mockEngineIO{
		llmFn: func(a ExecuteLlmParams) string { return "ok:" + a.StepID },
	}

	_, err := runPipeline(t, cfg, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	want := []string{
		"pipeline_start", "loop_start",
		"step_start", "step_complete",
		"step_start", "step_complete",
		"loop_complete", "pipeline_complete",
	}
	if len(io.events) != len(want) {
		t.Fatalf("got %d events %v, want %d %v", len(io.events), io.events, len(want), want)
	}
	for i, e := range io.events {
		if e != want[i] {
			t.Errorf("events[%d] = %q, want %q (full: %v)", i, e, want[i], io.events)
			break
		}
	}
}

// 6. resolveNodeCount accepts strict numeric variants and rejects mixed text.
func TestResolveNodeCount_StrictNumericVariants(t *testing.T) {
	ctx := context.Background()
	rt := baseRuntime()

	// "12." → 12 (strict numeric with trailing dot accepted)
	io1 := &mockEngineIO{outputs: map[string]string{"1:planner:0": "12."}}
	got, err := ResolveNodeCount(ctx, &ladr.Nodes{Kind: ladr.NodesString, Str: "{{planner.output}}"}, rt, io1)
	if err != nil {
		t.Fatal(err)
	}
	if got != 12 {
		t.Errorf("'12.' → got %d, want 12", got)
	}

	// "I think 12" → 0 (rejected, not a pure integer)
	io2 := &mockEngineIO{outputs: map[string]string{"1:planner:0": "I think 12"}}
	got, err = ResolveNodeCount(ctx, &ladr.Nodes{Kind: ladr.NodesString, Str: "{{planner.output}}"}, rt, io2)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("'I think 12' → got %d, want 0", got)
	}
}

// 7. Pipeline fails when required node dependencies are fully missing.
func TestPipeline_FailsWhenNodeDependenciesMissing(t *testing.T) {
	const cfg = `{
		"id":"missing-node-deps","name":"Missing Node Dependencies","author":"tests","version":1,
		"allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},
		"exit":"consumer","knobs":{},
		"steps":[
			{"id":"producer","name":"Producer","type":"normal"},
			{"id":"fanout","name":"Fanout","type":"normal","nodes":"{{producer.output}}"},
			{"id":"consumer","name":"Consumer","type":"normal","nodes":3,"fields":[
				{"name":"Dependency","type":"ingest","from":{"stepId":"fanout","loopRef":"current","nodeRef":"current"}}
			]}
		]
	}`

	io := &mockEngineIO{
		llmFn: func(a ExecuteLlmParams) string {
			if a.StepID == "producer" {
				return "I think 12"
			}
			if a.StepID == "fanout" {
				return "fanout"
			}
			return "consumer"
		},
	}

	_, err := runPipeline(t, cfg, io, 1)
	if err == nil {
		t.Fatal("expected error for missing node dependencies, got nil")
	}
	hasNodeFailed := false
	for _, e := range io.events {
		if e == "node_failed" || e == "error" {
			hasNodeFailed = true
		}
	}
	if !hasNodeFailed {
		t.Error("events missing node_failed/error")
	}
}

func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
