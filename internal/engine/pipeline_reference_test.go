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
	"sync/atomic"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

// Reference-mode reliks must exist at run time. A missing reference fails the
// run BEFORE any LLM call — a wiring bug should cost nothing.

func referenceTestConfig(mode string) *ladr.PipelineConfig {
	decls := []ladr.RelikDecl{{
		Title: "shared",
		Mode:  mode,
		Folders: []ladr.FolderDecl{{
			Name: "facts", TokenLimit: 100,
			Files: []ladr.FileDecl{{Name: "core.md", Init: "blank"}},
		}},
	}}
	if mode == ladr.RelikModeReference {
		decls[0].Folders = nil
	}
	return &ladr.PipelineConfig{
		Name:           "relik-ref",
		Exit:           "go",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Knobs:          map[string]ladr.Knob{},
		Reliks:         decls,
		Steps: []ladr.Step{
			{
				Kind: ladr.StepTypeNormal,
				Normal: &ladr.NormalStep{
					BaseStep: ladr.BaseStep{
						ID: "go", Name: "go", Type: ladr.StepTypeNormal, SystemPrompt: "x",
						Nodes: &ladr.Nodes{Kind: ladr.NodesNumber, Num: 1},
					},
				},
			},
		},
	}
}

func TestExecutePipeline_ReferenceRelikMissingFailsFast(t *testing.T) {
	store := relik.NewMemoryStore()
	var llmCalls atomic.Int32
	io := &mockEngineIO{llmFn: func(a ExecuteLlmParams) string {
		llmCalls.Add(1)
		return `{}`
	}}
	_, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:     referenceTestConfig(ladr.RelikModeReference),
		Context:    ExecutionRuntimeContext{InstanceID: "ref-miss", Inputs: map[string]any{}, Knobs: map[string]float64{}},
		IO:         io,
		RelikStore: store,
		MaxLoops:   1,
	})
	if err == nil {
		t.Fatal("expected error for missing referenced relik")
	}
	if !strings.Contains(err.Error(), `"shared"`) {
		t.Errorf("error should name the missing relik, got: %v", err)
	}
	if n := llmCalls.Load(); n != 0 {
		t.Errorf("reference failure must precede any LLM call, made %d", n)
	}
}

func TestExecutePipeline_ReferenceRelikPresentRuns(t *testing.T) {
	store := relik.NewMemoryStore()
	// The owning ladder declares and seeds "shared"; the referencing config
	// then binds it by title.
	if err := store.Load(referenceTestConfig(ladr.RelikModeDeclare).Reliks); err != nil {
		t.Fatalf("seed load: %v", err)
	}
	io := &mockEngineIO{llmFn: func(a ExecuteLlmParams) string { return `{}` }}
	if _, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:     referenceTestConfig(ladr.RelikModeReference),
		Context:    ExecutionRuntimeContext{InstanceID: "ref-hit", Inputs: map[string]any{}, Knobs: map[string]float64{}},
		IO:         io,
		RelikStore: store,
		MaxLoops:   1,
	}); err != nil {
		t.Fatalf("run with present reference: %v", err)
	}
}

// Declared reliks seed idempotently — the auto-init half of the duality.
func TestExecutePipeline_DeclaredRelikAutoInits(t *testing.T) {
	store := relik.NewMemoryStore()
	io := &mockEngineIO{llmFn: func(a ExecuteLlmParams) string { return `{}` }}
	if _, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:     referenceTestConfig(ladr.RelikModeDeclare),
		Context:    ExecutionRuntimeContext{InstanceID: "decl", Inputs: map[string]any{}, Knobs: map[string]float64{}},
		IO:         io,
		RelikStore: store,
		MaxLoops:   1,
	}); err != nil {
		t.Fatalf("declared run: %v", err)
	}
	if !store.HasRelik("shared") {
		t.Fatal("declared relik should be seeded after run")
	}
}
