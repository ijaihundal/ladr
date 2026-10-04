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
	"reflect"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// Ported from tests/unit/resolve-field.test.ts.

func fieldRuntime() *ExecutionRuntimeContext {
	node := 3
	return &ExecutionRuntimeContext{
		InstanceID:        "instance",
		LoopIndex:         2,
		Depth:             0,
		CurrentStepID:     "step-current",
		CurrentNodeNumber: &node,
		Inputs:            map[string]any{"context": "How do we test?"},
		Knobs:             map[string]float64{"coverage": 12},
	}
}

func fieldRuntimeWith(node, loop int) *ExecutionRuntimeContext {
	rt := fieldRuntime()
	rt.CurrentNodeNumber = &node
	rt.LoopIndex = loop
	return rt
}

func loopRef(s string) ladr.LoopRef {
	return ladr.LoopRef{Kind: ladr.LoopRefString, Str: s}
}
func loopRefNum(n float64) ladr.LoopRef {
	return ladr.LoopRef{Kind: ladr.LoopRefNumber, Num: n}
}

func TestResolveField_TextValue(t *testing.T) {
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldText,
		Text: ladr.TextField{Name: "Context", From: "context"},
	}, fieldRuntime(), &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}
	if v != "How do we test?" {
		t.Errorf("got %v, want 'How do we test?'", v)
	}
}

func TestResolveField_KnobInfoValue(t *testing.T) {
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind:     ladr.FieldKnobInfo,
		KnobInfo: ladr.KnobInfoField{Name: "Coverage", From: "coverage"},
	}, fieldRuntime(), &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}
	if v != "12" {
		t.Errorf("got %v, want '12'", v)
	}
}

func TestResolveField_NodeInfoValue(t *testing.T) {
	rt := fieldRuntimeWith(4, 2)
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind:     ladr.FieldNodeInfo,
		NodeInfo: ladr.NodeInfoField{Name: "Node"},
	}, rt, &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}
	if v != "4" {
		t.Errorf("got %v, want '4'", v)
	}
}

func TestResolveField_IngestCurrentLoop(t *testing.T) {
	io := &mockEngineIO{outputs: map[string]string{"2:draft:0": "draft-l2"}}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldIngest,
		Ingest: ladr.IngestField{
			Name: "Draft",
			From: ladr.FieldSourceRef{StepID: "draft", LoopRef: loopRef("current")},
		},
	}, fieldRuntimeWith(3, 2), io)
	if err != nil {
		t.Fatal(err)
	}
	if v != "draft-l2" {
		t.Errorf("got %v, want 'draft-l2'", v)
	}
}

func TestResolveField_IngestPreviousLoop(t *testing.T) {
	io := &mockEngineIO{outputs: map[string]string{"1:draft:0": "draft-l1"}}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldIngest,
		Ingest: ladr.IngestField{
			Name: "Draft",
			From: ladr.FieldSourceRef{StepID: "draft", LoopRef: loopRef("previous")},
		},
	}, fieldRuntimeWith(3, 2), io)
	if err != nil {
		t.Fatal(err)
	}
	if v != "draft-l1" {
		t.Errorf("got %v, want 'draft-l1'", v)
	}
}

func TestResolveField_IngestFixedLoopIndex(t *testing.T) {
	io := &mockEngineIO{outputs: map[string]string{"0:seed:0": "seed-l0"}}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldIngest,
		Ingest: ladr.IngestField{
			Name: "Seed",
			From: ladr.FieldSourceRef{StepID: "seed", LoopRef: loopRefNum(0)},
		},
	}, fieldRuntimeWith(3, 5), io)
	if err != nil {
		t.Fatal(err)
	}
	if v != "seed-l0" {
		t.Errorf("got %v, want 'seed-l0'", v)
	}
}

func TestResolveField_IngestCurrentNode(t *testing.T) {
	io := &mockEngineIO{outputs: map[string]string{"2:draft:3": "draft-node-3"}}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldIngest,
		Ingest: ladr.IngestField{
			Name: "Node Draft",
			From: ladr.FieldSourceRef{StepID: "draft", LoopRef: loopRef("current"), NodeRef: ladr.NodeRefCurrent},
		},
	}, fieldRuntimeWith(3, 2), io)
	if err != nil {
		t.Fatal(err)
	}
	if v != "draft-node-3" {
		t.Errorf("got %v, want 'draft-node-3'", v)
	}
}

func TestResolveField_IngestPreviousNode(t *testing.T) {
	io := &mockEngineIO{outputs: map[string]string{"2:draft:3": "draft-node-3"}}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldIngest,
		Ingest: ladr.IngestField{
			Name: "Node Draft",
			From: ladr.FieldSourceRef{StepID: "draft", LoopRef: loopRef("current"), NodeRef: ladr.NodeRefPrevious},
		},
	}, fieldRuntimeWith(4, 2), io)
	if err != nil {
		t.Fatal(err)
	}
	if v != "draft-node-3" {
		t.Errorf("got %v, want 'draft-node-3'", v)
	}
}

func TestResolveField_IngestPreviousNodeOnFirstNodeFallback(t *testing.T) {
	io := &mockEngineIO{outputs: map[string]string{"2:draft:1": "draft-node-1"}}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldIngest,
		Ingest: ladr.IngestField{
			Name: "Node Draft",
			From: ladr.FieldSourceRef{StepID: "draft", LoopRef: loopRef("current"), NodeRef: ladr.NodeRefPrevious},
		},
	}, fieldRuntimeWith(1, 2), io)
	if err != nil {
		t.Fatal(err)
	}
	if v != "draft-node-1" {
		t.Errorf("got %v, want 'draft-node-1' (first node falls back to self)", v)
	}
}

func TestResolveField_IngestSkipFirstNode(t *testing.T) {
	io := &mockEngineIO{outputs: map[string]string{"2:draft:1": "should-not-read"}}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldIngest,
		Ingest: ladr.IngestField{
			Name:          "Prev",
			From:          ladr.FieldSourceRef{StepID: "draft", LoopRef: loopRef("current"), NodeRef: ladr.NodeRefPrevious},
			SkipFirstNode: true,
		},
	}, fieldRuntimeWith(1, 2), io)
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Errorf("got %v, want empty (skipFirstNode on first node)", v)
	}
}

func TestResolveField_MultiIngestMergesInOrder(t *testing.T) {
	io := &mockEngineIO{outputs: map[string]string{
		"2:stepA:0": "A-current",
		"1:stepB:0": "B-previous",
	}}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldMultiIngest,
		MultiIngest: ladr.MultiIngestField{
			Name: "Drafts",
			From: []ladr.FieldSourceRef{
				{StepID: "stepA", LoopRef: loopRef("current")},
				{StepID: "stepB", LoopRef: loopRef("previous")},
			},
		},
	}, fieldRuntimeWith(3, 2), io)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A-current", "B-previous"}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("got %v, want %v", v, want)
	}
}

func TestResolveField_MultiIngestLoopAccumulation(t *testing.T) {
	io := &mockEngineIO{outputs: map[string]string{
		"0:stepA:0": "A-l0",
		"1:stepA:0": "A-l1",
		"2:stepA:0": "A-l2",
	}}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldMultiIngest,
		MultiIngest: ladr.MultiIngestField{
			Name: "Drafts",
			From: []ladr.FieldSourceRef{
				{StepID: "stepA", LoopRef: loopRef("accumulate")},
			},
		},
	}, fieldRuntimeWith(3, 3), io)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A-l0", "A-l1", "A-l2"}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("got %v, want %v", v, want)
	}
}

func TestResolveField_MultiIngestNodeAccumulation(t *testing.T) {
	io := &mockEngineIO{
		outputs: map[string]string{
			"2:judge:1": "1",
			"2:judge:2": "0",
			"2:judge:3": "1",
		},
		nodeCounts: map[string]int{"2:judge": 3},
	}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldMultiIngest,
		MultiIngest: ladr.MultiIngestField{
			Name: "Drafts",
			From: []ladr.FieldSourceRef{
				{StepID: "judge", LoopRef: loopRef("current"), NodeRef: ladr.NodeRefAccumulate},
			},
		},
	}, fieldRuntimeWith(3, 2), io)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "0", "1"}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("got %v, want %v", v, want)
	}
}

func TestResolveField_MultiIngestLoopAndNodeAccumulation(t *testing.T) {
	io := &mockEngineIO{
		outputs: map[string]string{
			"0:judge:1": "l0-n1",
			"0:judge:2": "l0-n2",
			"1:judge:1": "l1-n1",
			"1:judge:2": "l1-n2",
		},
		nodeCounts: map[string]int{"0:judge": 2, "1:judge": 2},
	}
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldMultiIngest,
		MultiIngest: ladr.MultiIngestField{
			Name: "Drafts",
			From: []ladr.FieldSourceRef{
				{StepID: "judge", LoopRef: loopRef("accumulate"), NodeRef: ladr.NodeRefAccumulate},
			},
		},
	}, fieldRuntimeWith(3, 2), io)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"l0-n1", "l0-n2", "l1-n1", "l1-n2"}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("got %v, want %v", v, want)
	}
}

func TestResolveField_MissingIngestSourceReturnsEmpty(t *testing.T) {
	v, err := ResolveField(context.Background(), ladr.Field{
		Kind: ladr.FieldIngest,
		Ingest: ladr.IngestField{
			Name: "Draft",
			From: ladr.FieldSourceRef{StepID: "missing", LoopRef: loopRef("current")},
		},
	}, fieldRuntime(), &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Errorf("got %v, want empty string", v)
	}
}
