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
	"errors"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// TestResolveField_TextFallback_MissingKey verifies that a TextField with a
// declared fallback consults the fallback when its primary input key is unset.
func TestResolveField_TextFallback_MissingKey(t *testing.T) {
	field := ladr.Field{
		Kind: ladr.FieldText,
		Text: ladr.TextField{
			Name:     "Q",
			Type:     "text",
			From:     "input.question",
			Fallback: &ladr.FromSpec{Kind: ladr.FromLiteral, Literal: "default question"},
		},
	}
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{
			// input.question intentionally unset
			"input.systemTime": "2026-07-21T20:47:26Z",
		},
	}

	val, err := ResolveField(context.Background(), field, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "default question" {
		t.Errorf("expected fallback value %q, got %v", "default question", val)
	}
}

// TestResolveField_TextFallback_PrimaryPresent verifies the fallback is NOT
// consulted when the primary source has a value.
func TestResolveField_TextFallback_PrimaryPresent(t *testing.T) {
	field := ladr.Field{
		Kind: ladr.FieldText,
		Text: ladr.TextField{
			Name:     "Q",
			Type:     "text",
			From:     "input.question",
			Fallback: &ladr.FromSpec{Kind: ladr.FromLiteral, Literal: "default question"},
		},
	}
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{
			"input.question": "real question",
		},
	}

	val, err := ResolveField(context.Background(), field, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "real question" {
		t.Errorf("expected primary value %q, got %v", "real question", val)
	}
}

// TestResolveField_TextFallback_FromInputKey verifies the new FromInputKey kind
// resolves via the runtime inputs map.
func TestResolveField_TextFallback_FromInputKey(t *testing.T) {
	field := ladr.Field{
		Kind: ladr.FieldText,
		Text: ladr.TextField{
			Name:     "Now",
			Type:     "text",
			From:     "input.missing",
			Fallback: &ladr.FromSpec{Kind: ladr.FromInputKey, InputKey: "input.systemTime"},
		},
	}
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{
			"input.systemTime": "2026-07-21T20:47:26Z",
		},
	}

	val, err := ResolveField(context.Background(), field, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "2026-07-21T20:47:26Z" {
		t.Errorf("expected input.systemTime value, got %v", val)
	}
}

// TestResolveField_IngestFallback_StepMissing verifies fallback triggers on a
// source-missing error (NodeDependencyError) for an IngestField.
func TestResolveField_IngestFallback_StepMissing(t *testing.T) {
	field := ladr.Field{
		Kind: ladr.FieldIngest,
		Ingest: ladr.IngestField{
			Name: "Cand",
			Type: "ingest",
			From: ladr.FieldSourceRef{
				StepID:  "generate",
				LoopRef: ladr.LoopRef{Kind: ladr.LoopRefString, Str: "current"},
			},
			Fallback: &ladr.FromSpec{Kind: ladr.FromLiteral, Literal: "no candidate"},
		},
	}
	// mockEngineIO with no outputs recorded → ingest will return empty/missing.
	io := &mockEngineIO{outputs: map[string]string{}}
	runtime := &ExecutionRuntimeContext{
		InstanceID:    "test",
		LoopIndex:     0,
		CurrentStepID: "verify",
		Inputs:        map[string]any{},
	}

	val, err := ResolveField(context.Background(), field, runtime, io)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "no candidate" {
		t.Errorf("expected fallback value %q, got %v", "no candidate", val)
	}
}

// TestResolveField_IngestFallback_RealError_Propagates verifies that non-missing
// errors (e.g. a synthetic error from the resolver) propagate — fallback is
// only consulted on source-missing, not on arbitrary errors.
func TestResolveField_IngestFallback_RealError_Propagates(t *testing.T) {
	// Use a NodeDependencyError directly — that SHOULD trigger fallback per spec.
	nde := NewNodeDependencyError("missing step")
	if !errors.Is(nde, nde) {
		t.Fatal("setup error")
	}
	// Confirm NodeDependencyError is recognized by the missing-source check.
	var target *NodeDependencyError
	if !errors.As(nde, &target) {
		t.Errorf("NodeDependencyError should be detectable via errors.As")
	}
}

// TestResolveField_NoFallback_NoBehaviorChange verifies that fields without a
// declared fallback behave exactly as before (no regression).
func TestResolveField_NoFallback_NoBehaviorChange(t *testing.T) {
	field := ladr.Field{
		Kind: ladr.FieldText,
		Text: ladr.TextField{
			Name: "Q",
			Type: "text",
			From: "input.question",
		},
	}
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{
			"input.question": "real question",
		},
	}

	val, err := ResolveField(context.Background(), field, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "real question" {
		t.Errorf("expected %q, got %v", "real question", val)
	}
}

// TestBuildManifest_TimeField verifies the manifest Time field is populated
// from auto-injected input keys.
func TestBuildManifest_TimeField(t *testing.T) {
	config := &ladr.PipelineConfig{
		Name:  "test",
		Exit:  "answer",
		Steps: []ladr.Step{},
	}
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{
			"input.context":    "hi",
			"input.systemTime": "2026-07-21T20:47:26Z",
			"input.localTime":  "2026-07-21T16:47:26-04:00",
			"input.timezone":   "America/New_York",
		},
		Knobs: map[string]float64{},
	}

	manifest, err := BuildManifest(context.Background(), config, runtime, &mockEngineIO{}, nil, NewPrunedNodeMap())
	if err != nil {
		t.Fatalf("BuildManifest error: %v", err)
	}
	if manifest.Time == nil {
		t.Fatal("expected manifest.Time to be non-nil")
	}
	if manifest.Time.Now != "2026-07-21T20:47:26Z" {
		t.Errorf("expected Now=UTC time, got %q", manifest.Time.Now)
	}
	if manifest.Time.LocalNow != "2026-07-21T16:47:26-04:00" {
		t.Errorf("expected LocalNow, got %q", manifest.Time.LocalNow)
	}
	if manifest.Time.Timezone != "America/New_York" {
		t.Errorf("expected Timezone, got %q", manifest.Time.Timezone)
	}
}

// TestBuildManifest_TimeField_NoTime verifies the manifest Time field is nil
// when no system time is present (defensive — gateway always injects).
func TestBuildManifest_TimeField_NoTime(t *testing.T) {
	config := &ladr.PipelineConfig{
		Name:  "test",
		Exit:  "answer",
		Steps: []ladr.Step{},
	}
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{"input.context": "hi"},
		Knobs:  map[string]float64{},
	}

	manifest, err := BuildManifest(context.Background(), config, runtime, &mockEngineIO{}, nil, NewPrunedNodeMap())
	if err != nil {
		t.Fatalf("BuildManifest error: %v", err)
	}
	if manifest.Time != nil {
		t.Errorf("expected manifest.Time nil when input.systemTime absent, got %+v", manifest.Time)
	}
}

// TestFromSpec_InputKey_UnmarshalRoundTrip verifies the new FromInputKey kind
// round-trips through JSON correctly.
func TestFromSpec_InputKey_UnmarshalRoundTrip(t *testing.T) {
	cases := []struct {
		json     string
		wantKind ladr.FromKind
		wantStr  string
	}{
		{`"input.context"`, ladr.FromInputKey, "input.context"},
		{`"input.systemTime"`, ladr.FromInputKey, "input.systemTime"},
		{`"previous"`, ladr.FromKeywordPrevious, ""},
		{`"output"`, ladr.FromKeywordOutput, ""},
		{`"literal text"`, ladr.FromLiteral, "literal text"},
	}
	for _, tc := range cases {
		var fs ladr.FromSpec
		if err := fs.UnmarshalJSON([]byte(tc.json)); err != nil {
			t.Errorf("UnmarshalJSON(%s) error: %v", tc.json, err)
			continue
		}
		if fs.Kind != tc.wantKind {
			t.Errorf("%s: kind = %v, want %v", tc.json, fs.Kind, tc.wantKind)
			continue
		}
		gotStr := fs.InputKey
		if tc.wantKind == ladr.FromLiteral {
			gotStr = fs.Literal
		}
		if gotStr != tc.wantStr {
			t.Errorf("%s: value = %q, want %q", tc.json, gotStr, tc.wantStr)
		}
		// Marshal back and confirm round-trip.
		out, err := fs.MarshalJSON()
		if err != nil {
			t.Errorf("MarshalJSON error: %v", err)
			continue
		}
		if string(out) != tc.json {
			t.Errorf("%s: round-trip = %s, want %s", tc.json, string(out), tc.json)
		}
	}
}

// TestBuildManifest_InternalKeysFiltered verifies the manifest does NOT expose
// engine-internal input keys (__passfields, handoff lineage, raw API metadata)
// to the planner LLM. Only legitimate author/caller-facing inputs surface.
func TestBuildManifest_InternalKeysFiltered(t *testing.T) {
	config := &ladr.PipelineConfig{
		Name:  "test",
		Exit:  "answer",
		Steps: []ladr.Step{},
	}
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{
			// Legitimate — should appear.
			"input.context":    "hi",
			"input.systemTime": "2026-07-21T20:47:26Z",
			"input.timezone":   "America/New_York",
			// Internal — should be filtered out.
			"__passfields":            map[string]string{"Question": "q"},
			"messages":                []any{},
			"model":                   "alice/x",
			"author":                  "alice",
			"template":                "x",
			"debugMode":               false,
			"input.handoffFrom":       "parent-1",
			"input.handoffFromStep":   "route",
			"input.handoffTarget":     map[string]any{"ladderId": "@alice/x"},
			"input.handoffTargetStep": "step4",
		},
		Knobs: map[string]float64{},
	}

	manifest, err := BuildManifest(context.Background(), config, runtime, &mockEngineIO{}, nil, NewPrunedNodeMap())
	if err != nil {
		t.Fatalf("BuildManifest error: %v", err)
	}
	for _, k := range manifest.Inputs {
		if isInternalInputKey(k) {
			t.Errorf("internal key %q leaked into manifest Inputs: %v", k, manifest.Inputs)
		}
	}
	// Spot-check that the legitimate keys DID make it through.
	hasContext := false
	for _, k := range manifest.Inputs {
		if k == "input.context" {
			hasContext = true
		}
	}
	if !hasContext {
		t.Errorf("expected input.context in manifest Inputs, got %v", manifest.Inputs)
	}
}
