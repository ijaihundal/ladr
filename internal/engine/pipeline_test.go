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
	"strings"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// Ported from tests/unit/engine-core.test.ts.

func mustParseConfig(t *testing.T, jsonStr string) *ladr.PipelineConfig {
	t.Helper()
	cfg, err := ladr.ParseStoredConfig([]byte(jsonStr))
	if err != nil {
		t.Fatalf("ParseStoredConfig: %v", err)
	}
	return cfg
}

const twoStepConfig = `{
  "id":"demo","name":"Demo","author":"tests","version":1,
  "allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},
  "exit":"step2","knobs":{},
  "steps":[
    {"id":"step1","name":"Step 1","type":"normal","fields":[{"name":"Context","type":"text","from":"context"}],"systemPrompt":"First step"},
    {"id":"step2","name":"Step 2","type":"normal","fields":[{"name":"Input","type":"ingest","from":{"stepId":"step1","loopRef":"current"}}],"systemPrompt":"Second step"}
  ]
}`

func TestExecutePipeline_TwoStepFlow(t *testing.T) {
	cfg := mustParseConfig(t, twoStepConfig)
	io := &mockEngineIO{
		llmFn: func(a ExecuteLlmParams) string { return a.StepID + ":" + a.UserPrompt },
	}

	result, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:   cfg,
		Context:  ExecutionRuntimeContext{InstanceID: "instance-2", Depth: 0, Inputs: map[string]any{"context": "hello"}},
		IO:       io,
		Provider: ProviderConfig{Provider: "openrouter", Model: "gpt-oss-20b", APIKey: "secret"},
		MaxLoops: 1,
	})
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if !strings.HasPrefix(result, "step2:") {
		t.Errorf("result = %q, want prefix 'step2:'", result)
	}
	found := false
	for _, e := range io.events {
		if e == "pipeline_complete" {
			found = true
		}
	}
	if !found {
		t.Error("events missing 'pipeline_complete'")
	}
}

const continueIfFailConfig = `{
  "id":"gate","name":"Gate","author":"tests","version":1,
  "allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},
  "exit":"check","knobs":{},
  "steps":[
    {"id":"check","name":"Check","type":"normal","continueIf":"PASS"}
  ]
}`

func TestExecutePipeline_ContinueIfFails(t *testing.T) {
	cfg := mustParseConfig(t, continueIfFailConfig)
	io := &mockEngineIO{
		llmFn: func(a ExecuteLlmParams) string { return "FAIL" },
	}

	_, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:   cfg,
		Context:  ExecutionRuntimeContext{InstanceID: "instance-gate-fail", Depth: 0, Inputs: map[string]any{}},
		IO:       io,
		Provider: ProviderConfig{Provider: "openrouter", Model: "gpt-oss-20b", APIKey: "secret"},
		MaxLoops: 1,
	})
	if err == nil {
		t.Fatal("expected error from failed continueIf gate, got nil")
	}
	if !strings.Contains(err.Error(), "continueIf") {
		t.Errorf("err = %v, want it to mention continueIf", err)
	}
	found := false
	for _, e := range io.events {
		if e == "error" {
			found = true
		}
	}
	if !found {
		t.Error("events missing 'error'")
	}
}

const prunedFanoutConfig = `{
  "id":"tot","name":"ToT","author":"tests","version":1,
  "allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},
  "exit":"refine","knobs":{},
  "steps":[
    {"id":"evaluate","name":"Evaluate","type":"normal","nodes":3,"continueIf":"1"},
    {"id":"refine","name":"Refine","type":"normal","nodes":{"from":{"stepId":"evaluate","loopRef":"current","pruned":true}},"fields":[{"name":"Seed","type":"ingest","from":{"stepId":"evaluate","loopRef":"current","nodeRef":"current"}}]}
  ]
}`

func TestExecutePipeline_ContinueIfPruningWithPrunedFanout(t *testing.T) {
	cfg := mustParseConfig(t, prunedFanoutConfig)
	io := &mockEngineIO{
		llmFn: func(a ExecuteLlmParams) string {
			if a.StepID == "evaluate" {
				if a.NodeNumber != nil && *a.NodeNumber == 2 {
					return "0"
				}
				return "1"
			}
			return "seen:" + a.UserPrompt
		},
	}

	result, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:   cfg,
		Context:  ExecutionRuntimeContext{InstanceID: "instance-tot", Depth: 0, Inputs: map[string]any{}},
		IO:       io,
		Provider: ProviderConfig{Provider: "openrouter", Model: "gpt-oss-20b", APIKey: "secret"},
		MaxLoops: 1,
	})
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	// Node 2 (which returned "0") is pruned; nodes 1 and 3 survive into refine.
	r1 := io.writes["0:refine:1"]
	r2 := io.writes["0:refine:2"]
	r3 := io.writes["0:refine:3"]
	if !strings.HasPrefix(r1, "seen:") {
		t.Errorf("refine node 1 = %q, want 'seen:' prefix", r1)
	}
	if !strings.HasPrefix(r2, "seen:") {
		t.Errorf("refine node 2 = %q, want 'seen:' prefix", r2)
	}
	if r3 != "" {
		t.Errorf("refine node 3 = %q, want empty (pruned)", r3)
	}
	if !strings.HasPrefix(result, "seen:") {
		t.Errorf("result = %q, want 'seen:' prefix", result)
	}
	if !strings.Contains(result, "\n\nseen:") {
		t.Errorf("result = %q, want multi-node output joined by blank line", result)
	}
}
