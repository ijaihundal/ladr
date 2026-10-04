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

package trace

import (
	"math"
	"strconv"
	"strings"

	"github.com/ladr/ladr/internal/ladr"
)

// LeafStep is a NormalStep or SequentialStep — any step that can produce node
// outputs. Group steps are never leaf steps.
type LeafStep struct {
	Kind ladr.StepType
	Base *ladr.BaseStep
}

// ID returns the step id.
func (l LeafStep) ID() string {
	if l.Base != nil {
		return l.Base.ID
	}
	return ""
}

// Name returns the step name, falling back to the id.
func (l LeafStep) Name() string {
	if l.Base != nil {
		if l.Base.Name != "" {
			return l.Base.Name
		}
		return l.Base.ID
	}
	return ""
}

// Timeline returns the step's timeline annotation (may be nil).
func (l LeafStep) Timeline() *ladr.TimelineKind {
	if l.Base != nil {
		return l.Base.Timeline
	}
	return nil
}

// Checkpoint returns the step's checkpoint config (may be nil).
func (l LeafStep) Checkpoint() *ladr.Checkpoint {
	if l.Base != nil {
		return l.Base.Checkpoint
	}
	return nil
}

// LeafStepEntry pairs a leaf step with the optional group it belongs to.
type LeafStepEntry struct {
	Step    LeafStep
	GroupID string // empty when the step is top-level
}

// FlattenLeafSteps flattens a config's step tree into leaf entries. Group
// children are annotated with the group id. Nested groups (which are
// rejected by validation) are silently skipped.
func FlattenLeafSteps(steps []ladr.Step) []LeafStepEntry {
	var out []LeafStepEntry
	for _, s := range steps {
		if s.Kind == ladr.StepTypeGroup && s.Group != nil {
			for _, child := range s.Group.Steps {
				if child.Kind == ladr.StepTypeGroup {
					continue
				}
				out = append(out, LeafStepEntry{
					Step:    LeafStep{Kind: child.Kind, Base: child.Base()},
					GroupID: s.Group.ID,
				})
			}
		} else {
			out = append(out, LeafStepEntry{
				Step: LeafStep{Kind: s.Kind, Base: s.Base()},
			})
		}
	}
	return out
}

// GetTraceConfig returns the config's TraceConfig, or a zero-value default.
func GetTraceConfig(config *ladr.PipelineConfig) ladr.TraceConfig {
	if config.Trace != nil {
		return *config.Trace
	}
	return ladr.TraceConfig{}
}

// GetTraceMode returns the trace mode, defaulting to "reasoning".
func GetTraceMode(config *ladr.PipelineConfig) ladr.TraceMode {
	tc := GetTraceConfig(config)
	if tc.Mode == "" {
		return ladr.TraceModeReasoning
	}
	return tc.Mode
}

// ShowAllSteps is deprecated. Timeline annotations (circle/init) are now the
// sole selection mechanism. Kept for backward-compat — always returns false.
func ShowAllSteps(config *ladr.PipelineConfig) bool {
	return false
}

// IsExitStep returns true if the leaf step is the configured exit step.
func IsExitStep(step LeafStep, config *ladr.PipelineConfig) bool {
	return step.ID() == config.Exit
}

// IsCheckpoint returns true if the step has a checkpoint timeline
// (init or circle) and is not the exit step.
func IsCheckpoint(step LeafStep, config *ladr.PipelineConfig) bool {
	if IsExitStep(step, config) {
		return false
	}
	tl := step.Timeline()
	if tl == nil {
		return false
	}
	return *tl == ladr.TimelineInit || *tl == ladr.TimelineCircle
}

// ShouldShowStep returns whether a step should appear in the trace output.
// Only steps with timeline: circle or timeline: init are shown.
func ShouldShowStep(step LeafStep, config *ladr.PipelineConfig) bool {
	if IsExitStep(step, config) {
		return false
	}
	return IsCheckpoint(step, config)
}

// GetCheckpointHeading returns the heading for a checkpoint step.
func GetCheckpointHeading(step LeafStep) string {
	cp := step.Checkpoint()
	if cp != nil && cp.Heading != "" {
		return cp.Heading
	}
	return step.Name()
}

// RenderNodeHeading formats the node heading using the step's checkpoint
// template, falling back to "{name} {n}".
func RenderNodeHeading(step LeafStep, nodeNumber int) string {
	tmpl := "{name} {n}"
	cp := step.Checkpoint()
	if cp != nil && cp.NodeHeading != "" {
		tmpl = cp.NodeHeading
	}
	n := strconv.Itoa(nodeNumber)
	result := strings.ReplaceAll(tmpl, "{n}", n)
	result = strings.ReplaceAll(result, "{name}", step.Name())
	result = strings.ReplaceAll(result, "{id}", step.ID())
	return result
}

// GetStepEntryByID returns the leaf step entry with the given step id.
func GetStepEntryByID(config *ladr.PipelineConfig, stepID string) (LeafStepEntry, bool) {
	for _, entry := range FlattenLeafSteps(config.Steps) {
		if entry.Step.ID() == stepID {
			return entry, true
		}
	}
	return LeafStepEntry{}, false
}

// ResolveMaxLoops determines the effective loop count from knob values.
// Checks for a knob with type "loops", then falls back to maxLoops/loops keys,
// then defaults to 1.
func ResolveMaxLoops(config *ladr.PipelineConfig, knobValues map[string]any) int {
	// Find the loop knob
	var loopKey string
	if config.Knobs != nil {
		for key, def := range config.Knobs {
			if def.Type == ladr.KnobTypeLoops {
				loopKey = key
				break
			}
		}
	}

	// Try the typed loop knob first
	if loopKey != "" {
		if v, ok := knobValues[loopKey]; ok {
			if n := toFinitePositiveInt(v); n > 0 {
				return n
			}
		}
	}

	// Fall back to maxLoops / loops keys
	for _, fallbackKey := range []string{"maxLoops", "loops"} {
		if v, ok := knobValues[fallbackKey]; ok {
			if n := toFinitePositiveInt(v); n > 0 {
				return n
			}
		}
	}

	return 1
}

// toFinitePositiveInt converts a knob value to a positive int (min 1).
// Returns 0 if the value is not a finite positive number.
func toFinitePositiveInt(v any) int {
	switch n := v.(type) {
	case float64:
		if isNaNOrInf(n) {
			return 0
		}
		i := int(n)
		if i < 1 {
			return 0
		}
		return i
	case int:
		if n < 1 {
			return 0
		}
		return n
	case int64:
		if n < 1 {
			return 0
		}
		return int(n)
	case string:
		i, err := strconv.Atoi(n)
		if err != nil || i < 1 {
			return 0
		}
		return i
	default:
		return 0
	}
}

func isNaNOrInf(f float64) bool {
	return math.IsNaN(f) || math.IsInf(f, 0)
}
