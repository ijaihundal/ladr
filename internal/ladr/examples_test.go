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

package ladr_test

import (
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// These four configs are JSON transcriptions of the canonical YAML examples in
// the LukiScript docs (LADR docs). They prove the documented examples parse
// AND validate cleanly end-to-end.

// Spec §1.5 — the minimal "hello" ladder (author/version present, stripped by
// ParseStoredConfig).
const specMinimalExample = `{
	"id":"hello","author":"LADR","version":1,
	"name":"Hello Ladder",
	"allowedTargets":{"strategy":"universal"},
	"exit":"step0",
	"knobs":{},
	"steps":[
		{"id":"step0","name":"Answer","type":"normal",
		 "fields":[{"name":"Context","type":"text","from":"input.context"}],
		 "systemPrompt":"Answer clearly and directly."}
	]
}`

// Example A — minimal two-step.
const minimalTwoStepExample = `{
	"name":"Mini Two-Step",
	"allowedTargets":{"strategy":"universal"},
	"exit":"step1",
	"knobs":{},
	"steps":[
		{"id":"step0","name":"Draft","type":"normal",
		 "fields":[{"name":"Context","type":"text","from":"input.context"}],
		 "systemPrompt":"Draft answer."},
		{"id":"step1","name":"Improve","type":"normal",
		 "fields":[{"name":"Draft","type":"ingest","from":{"stepId":"step0","loopRef":"current"}}],
		 "systemPrompt":"Improve clarity and structure."}
	]
}`

// Example B — gated Tree-of-Thought.
const gatedTotExample = `{
	"name":"Gated Tree of Thought",
	"allowedTargets":{"strategy":"universal"},
	"exit":"answer",
	"knobs":{
		"branches":{"name":"Branches","type":"nodes","input":"slider",
			"steps":[{"title":"Low","value":3,"default":true},{"title":"Mid","value":5},{"title":"High","value":7}]}
	},
	"steps":[
		{"id":"draft","name":"Draft","type":"normal",
		 "fields":[{"name":"Context","type":"text","from":"input.context"},
		           {"name":"Branch Count","type":"knobInfo","from":"branches"}],
		 "systemPrompt":"Draft {Branch Count} different reasoning branches for the given context. Label each branch clearly."},
		{"id":"evaluate","name":"Evaluate","type":"normal","nodes":"{{knobs.branches}}",
		 "fields":[{"name":"Draft","type":"ingest","from":{"stepId":"draft","loopRef":"current"}},
		           {"name":"Branch Number","type":"nodeInfo"},
		           {"name":"Total Branches","type":"knobInfo","from":"branches"}],
		 "systemPrompt":"Evaluate branch {Branch Number} of {Total Branches}. Output 1 if strong, 0 if weak.",
		 "continueIf":"1"},
		{"id":"synthesize","name":"Synthesize","type":"normal",
		 "fields":[{"name":"Draft","type":"ingest","from":{"stepId":"draft","loopRef":"current"}},
		           {"name":"Evaluations","type":"multi_ingest","from":[{"stepId":"evaluate","loopRef":"current","nodeRef":"accumulate"}]}],
		 "systemPrompt":"Synthesize the surviving branches from the draft into one coherent answer."},
		{"id":"answer","name":"Answer","type":"normal",
		 "fields":[{"name":"Draft","type":"ingest","from":{"stepId":"synthesize","loopRef":"current"}}],
		 "systemPrompt":"Return the synthesized answer as-is."}
	]
}`

// Example C — recursive multi-loop refinement.
const recursiveLoopExample = `{
	"name":"Recursive Multi-Loop Refinement",
	"allowedTargets":{"strategy":"universal"},
	"exit":"final",
	"knobs":{
		"rounds":{"name":"Rounds","type":"loops","input":"numerical","default":2,"min":1,"max":4},
		"iterations":{"name":"Iterations","type":"recursion","input":"numerical","default":2,"min":1,"max":3}
	},
	"steps":[
		{"id":"draft","name":"Draft","type":"normal",
		 "fields":[{"name":"Context","type":"text","from":"input.context"},
		           {"name":"Earlier Drafts","type":"multi_ingest","from":[{"stepId":"final","loopRef":"accumulate"}]}],
		 "systemPrompt":"Draft based on context and prior rounds."},
		{"id":"final","name":"Final","type":"normal",
		 "recursion":{"maxDepth":"{{knobs.iterations}}"},
		 "fields":[{"name":"Draft","type":"ingest","from":{"stepId":"draft","loopRef":"current"}}],
		 "systemPrompt":"Refine draft and return final response."}
	]
}`

func TestGoldenSpecMinimal(t *testing.T) {
	cfg := mustValidate(t, specMinimalExample)
	if cfg.Name != "Hello Ladder" {
		t.Errorf("name = %q", cfg.Name)
	}
	if cfg.Exit != "step0" || len(cfg.Steps) != 1 {
		t.Errorf("exit=%q steps=%d", cfg.Exit, len(cfg.Steps))
	}
	if cfg.ID != "hello" {
		t.Errorf("id = %q", cfg.ID)
	}
}

func TestGoldenMinimalTwoStep(t *testing.T) {
	cfg := mustValidate(t, minimalTwoStepExample)
	if cfg.Exit != "step1" {
		t.Errorf("exit = %q", cfg.Exit)
	}
	if len(cfg.Steps) != 2 {
		t.Fatalf("steps = %d", len(cfg.Steps))
	}
	// step1 ingests from step0
	s1 := cfg.Steps[1].Normal
	if s1 == nil || s1.Fields == nil || len(s1.Fields.Fields) != 1 {
		t.Fatal("step1 fields missing")
	}
	if s1.Fields.Fields[0].Kind != ladr.FieldIngest {
		t.Errorf("expected ingest field")
	}
}

func TestGoldenGatedToT(t *testing.T) {
	cfg := mustValidate(t, gatedTotExample)
	if cfg.Exit != "answer" {
		t.Errorf("exit = %q", cfg.Exit)
	}
	if len(cfg.Steps) != 4 {
		t.Fatalf("steps = %d", len(cfg.Steps))
	}
	bk := cfg.Knobs["branches"]
	if bk.Type != ladr.KnobTypeNodes || bk.Input.Kind != ladr.KnobInputSlider {
		t.Errorf("branches knob wrong: %+v", bk)
	}
	// evaluate step has continueIf gate + knob-driven nodes
	eval := cfg.Steps[1].Normal
	if eval == nil || eval.ContinueIf != "1" {
		t.Error("evaluate continueIf missing")
	}
	if eval.Nodes == nil || eval.Nodes.Kind != ladr.NodesString {
		t.Error("evaluate nodes not a string/knob ref")
	}
}

func TestGoldenRecursiveLoop(t *testing.T) {
	cfg := mustValidate(t, recursiveLoopExample)
	if cfg.Exit != "final" {
		t.Errorf("exit = %q", cfg.Exit)
	}
	if len(cfg.Steps) != 2 {
		t.Fatalf("steps = %d", len(cfg.Steps))
	}
	// one loops knob + one recursion knob
	var hasLoops, hasRecursion bool
	for _, k := range cfg.Knobs {
		switch k.Type {
		case ladr.KnobTypeLoops:
			hasLoops = true
		case ladr.KnobTypeRecursion:
			hasRecursion = true
		}
	}
	if !hasLoops || !hasRecursion {
		t.Errorf("missing knob types: loops=%v recursion=%v", hasLoops, hasRecursion)
	}
	// final step has recursion referencing the iterations knob
	final := cfg.Steps[1].Normal
	if final == nil || final.Recursion == nil || !final.Recursion.MaxDepthNumOrStr.IsStr {
		t.Error("final recursion maxDepth not a string/knob ref")
	}
}

// All four golden examples validated together as a regression group.
func TestGoldenAllValidate(t *testing.T) {
	for _, c := range []struct{ name, raw string }{
		{"spec-minimal", specMinimalExample},
		{"minimal-two-step", minimalTwoStepExample},
		{"gated-tot", gatedTotExample},
		{"recursive-loop", recursiveLoopExample},
		{"passfields-intra", passFieldsIntraExample},
		{"passfields-cross", passFieldsCrossExample},
		{"fallback-text", fallbackTextExample},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := mustParseStored(t, c.raw)
			v := ladr.ValidateConfig(cfg)
			if !v.Valid {
				t.Fatalf("golden %s failed validation: %v", c.name, v.Errors)
			}
		})
	}
}

// passFieldsIntraExample — intra-ladder jump with passFields wiring the route
// step's output into finalize's Question field. Demonstrates the grammar +
// the static validation path (passField.to must match a declared field on
// the target step).
const passFieldsIntraExample = `{
	"name":"PassFields Intra",
	"allowedTargets":{"strategy":"universal"},
	"exit":"finalize",
	"knobs":{},
	"steps":[
		{"id":"route","name":"Route","type":"normal",
		 "systemPrompt":"Output a question.",
		 "if":{"contains":"?","then":{
		   "jump":{"stepId":"finalize",
		           "passFields":[{"to":"Question","from":"output"}]}},
		   "else":"abort"}},
		{"id":"finalize","name":"Finalize","type":"normal",
		 "fields":[
		   {"name":"Question","type":"text","from":"input.default",
		    "fallback":"no question supplied"}
		 ],
		 "systemPrompt":"Answer {{Question}}."}
	]
}`

// passFieldsCrossExample — cross-ladder handoff with passFields. Static
// validation only checks caller-side `from` references; target field matching
// happens at handoff-load-time when the target config is fetched.
const passFieldsCrossExample = `{
	"name":"PassFields Cross",
	"allowedTargets":{"strategy":"universal"},
	"exit":"router",
	"knobs":{},
	"steps":[
		{"id":"router","name":"Router","type":"normal",
		 "systemPrompt":"Output GO.",
		 "if":{"equals":"GO","then":{
		   "jump":{"ladderId":"@alice/helper","stepId":"step4",
		           "passFields":[
		             {"to":"Question","from":{"stepId":"router"}},
		             {"to":"Context","from":"literal context"}
		           ]}},
		   "else":"abort"}}
	]
}`

// fallbackTextExample — TextField with a fallback literal used when the primary
// input key is missing. Validates the new fallback grammar.
const fallbackTextExample = `{
	"name":"Fallback Text",
	"allowedTargets":{"strategy":"universal"},
	"exit":"answer",
	"knobs":{},
	"steps":[
		{"id":"answer","name":"Answer","type":"normal",
		 "fields":[
		   {"name":"Topic","type":"text","from":"input.topic",
		    "fallback":"general"}
		 ],
		 "systemPrompt":"Answer about {{Topic}}."}
	]
}`
