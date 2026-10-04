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

// Package engine — dynamic step loader.
//
// Loads a step config at runtime from a declared source (FromSpec).
// Used by steps with dynamic: true to get their config from a planner step's output.
package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/mailbox"
	"github.com/ladr/ladr/internal/relik"
)

// ResolveFromSpec resolves a FromSpec to a string value, given the current runtime context.
//
// This is the general-purpose source resolver used by:
//   - dynamic step loading (DynamicFrom)
//   - gated writes (if/then write.from)
func ResolveFromSpec(
	ctx context.Context,
	from ladr.FromSpec,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
	store relik.Store,
	currentStepOutput string,
) (string, error) {
	switch from.Kind {
	case ladr.FromKeywordPrevious:
		// Read the output of the immediately preceding step.
		// We use the current step's ID to figure out what "previous" means.
		// In practice, the engine sets runtime.CurrentStepID before calling this,
		// and "previous" is the step before it in the ladder's step list.
		// For now, we read the last step that produced output.
		// This is a simplification — the full implementation needs step ordering.
		return readPreviousStepOutput(ctx, runtime, io)

	case ladr.FromKeywordOutput:
		return currentStepOutput, nil

	case ladr.FromInputKey:
		// Look up the input key in the runtime inputs map. Missing key → empty string.
		if runtime.Inputs == nil {
			return "", nil
		}
		return normalizeText(runtime.Inputs[from.InputKey]), nil

	case ladr.FromLiteral:
		return from.Literal, nil

	case ladr.FromStepRef:
		return readStepRef(ctx, from.Step, runtime, io)

	case ladr.FromRelikRef:
		return readRelikRef(ctx, from.Relik, store)

	case ladr.FromMailboxRef:
		// Explicit consume: drain the ladder's pending messages now.
		if runtime.Mailbox == nil {
			return "", nil
		}
		owner := runtime.OwnerID
		if owner == "" {
			owner = runtime.InstanceID
		}
		ladder := runtime.LadderName
		envelopes, err := runtime.Mailbox.Drain(ctx, owner, ladder, 0)
		if err != nil {
			return "", fmt.Errorf("mailbox drain: %w", err)
		}
		return mailbox.Format(envelopes), nil

	default:
		return "", fmt.Errorf("from: unknown source kind")
	}
}

// readPreviousStepOutput reads the output of the step immediately before the current one.
// Uses the LastStepOutput tracked by the pipeline loop.
func readPreviousStepOutput(ctx context.Context, runtime *ExecutionRuntimeContext, io EngineIO) (string, error) {
	if runtime.LastStepID == "" {
		return "", fmt.Errorf("from: previous — no previous step has executed yet")
	}
	// Use the tracked output (just written by the pipeline loop).
	if runtime.LastStepOutput != "" {
		return runtime.LastStepOutput, nil
	}
	// Fallback: read from EngineIO.
	return io.ReadOutput(ctx, ReadOutputArgs{
		InstanceID: runtime.InstanceID,
		LoopIndex:  runtime.LoopIndex,
		StepID:     runtime.LastStepID,
	})
}

// readStepRef reads the output of a specific step/node.
func readStepRef(ctx context.Context, ref ladr.StepFrom, runtime *ExecutionRuntimeContext, io EngineIO) (string, error) {
	loopIdx := runtime.LoopIndex
	args := ReadOutputArgs{
		InstanceID: runtime.InstanceID,
		LoopIndex:  loopIdx,
		StepID:     ref.StepID,
	}
	if ref.Node != nil {
		nodeNum := *ref.Node
		args.NodeNumber = &nodeNum
	} else if runtime.CurrentNodeNumber != nil && ref.NodeRef == ladr.NodeRefCurrent {
		args.NodeNumber = runtime.CurrentNodeNumber
	}
	return io.ReadOutput(ctx, args)
}

// readRelikRef reads content from a relik.
func readRelikRef(ctx context.Context, target ladr.RelikTarget, store relik.Store) (string, error) {
	if store == nil {
		return "", fmt.Errorf("from: relik — no relik store available")
	}
	if target.HasMetatag() {
		val, err := store.ReadMetatag(target.Relik, target.Folder, target.File, target.Metatag)
		if err != nil {
			return "", err
		}
		if val == nil {
			return "", nil
		}
		b, _ := json.Marshal(val)
		return string(b), nil
	}
	return store.ReadFile(target.Relik, target.Folder, target.File)
}

// ---------------------------------------------------------------------------
// Dynamic step loader
// ---------------------------------------------------------------------------

// LoadDynamicStep loads and validates a dynamic step config from a FromSpec.
//
// Steps:
//  1. Resolve the FromSpec to get raw config text.
//  2. Parse as JSON into DynamicStepConfig.
//  3. Validate: systemPrompt non-empty, fields non-empty, no nested dynamic.
//  4. Optionally validate field references against the manifest.
//
// Returns the loaded config, or an error if validation fails.
func LoadDynamicStep(
	ctx context.Context,
	step *ladr.BaseStep,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
	store relik.Store,
	manifest *Manifest,
) (*ladr.DynamicStepConfig, error) {
	if step.DynamicFrom == nil {
		return nil, fmt.Errorf("dynamic step %q: no from: declared", step.ID)
	}

	// 1. Resolve the source. If primary misses (empty/error) and a fallback
	//    is declared, consult the fallback before giving up.
	rawConfig, err := ResolveFromSpec(ctx, *step.DynamicFrom, runtime, io, store, "")
	if err != nil || rawConfig == "" {
		if step.DynamicFromFallback != nil {
			rawConfig, err = ResolveFromSpec(ctx, *step.DynamicFromFallback, runtime, io, store, "")
		}
		if err != nil {
			if step.DynamicFromFallback != nil {
				return nil, fmt.Errorf("dynamic step %q: failed to resolve fromFallback: %w", step.ID, err)
			}
			return nil, fmt.Errorf("dynamic step %q: failed to resolve from: %w", step.ID, err)
		}
	}

	if rawConfig == "" {
		return nil, fmt.Errorf("dynamic step %q: resolved config is empty", step.ID)
	}

	// 2. Parse as JSON.
	var cfg ladr.DynamicStepConfig
	if err := json.Unmarshal([]byte(rawConfig), &cfg); err != nil {
		return nil, fmt.Errorf("dynamic step %q: invalid JSON config: %w", step.ID, err)
	}

	// 3. Validate structure.
	if err := validateDynamicConfig(&cfg, step.ID); err != nil {
		return nil, fmt.Errorf("dynamic step %q: %w", step.ID, err)
	}

	// 4. Validate field references against manifest (if available).
	if manifest != nil {
		if err := validateDynamicFieldReferences(&cfg, manifest, step.ID); err != nil {
			return nil, fmt.Errorf("dynamic step %q: %w", step.ID, err)
		}
	}

	return &cfg, nil
}

// validateDynamicConfig checks the structural constraints of a loaded dynamic config.
func validateDynamicConfig(cfg *ladr.DynamicStepConfig, stepID string) error {
	if cfg.SystemPrompt == "" {
		return fmt.Errorf("generated config must have systemPrompt")
	}
	if len(cfg.Fields) == 0 {
		return fmt.Errorf("generated config must have at least 1 field")
	}
	if cfg.Nodes != nil && (*cfg.Nodes < 1 || *cfg.Nodes > 5) {
		return fmt.Errorf("generated config nodes must be 1-5 (got %d)", *cfg.Nodes)
	}

	// Under Path B, control flow (if/jump/recursion/retrieve) IS allowed in dynamic configs.
	// The only restriction: no nested dynamic: true.
	// (DynamicStepConfig doesn't even have a Dynamic field, so this is structurally enforced.)

	return nil
}

// validateDynamicFieldReferences checks that all field references in the generated config
// resolve to something in the current manifest.
func validateDynamicFieldReferences(cfg *ladr.DynamicStepConfig, manifest *Manifest, stepID string) error {
	// Build a set of known step IDs and relik titles from the manifest.
	knownSteps := make(map[string]bool)
	for _, s := range manifest.Steps {
		knownSteps[s.ID] = true
	}
	knownReliks := make(map[string]bool)
	for _, s := range manifest.Reliks {
		knownReliks[s.Title] = true
	}

	for _, field := range cfg.Fields {
		switch field.Kind {
		case ladr.FieldIngest:
			if !knownSteps[field.Ingest.From.StepID] {
				return fmt.Errorf("field %q references unknown step %q (not in manifest)",
					field.Ingest.Name, field.Ingest.From.StepID)
			}
		case ladr.FieldMultiIngest:
			for _, src := range field.MultiIngest.From {
				if !knownSteps[src.StepID] {
					return fmt.Errorf("field %q references unknown step %q (not in manifest)",
						field.MultiIngest.Name, src.StepID)
				}
			}
		case ladr.FieldRelikRead:
			if !knownReliks[field.RelikRead.From.Relik] {
				return fmt.Errorf("field %q references unknown relik %q (not in manifest)",
					field.RelikRead.Name, field.RelikRead.From.Relik)
			}
		}
	}

	return nil
}
