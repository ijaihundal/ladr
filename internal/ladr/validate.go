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

// Package ladr — pipeline configuration validation.
// Ported from packages/ladr/validate.ts — all 16 validation functions.
package ladr

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ValidateConfig runs the full validation suite and returns a ValidationResult.
func ValidateConfig(config *PipelineConfig) ValidationResult {
	var errors []string

	stepMap := buildStepMap(config.Steps)
	stepOrder := buildStepOrder(config.Steps)

	validateStepUniqueness(config.Steps, &errors)
	validateNoNestedGroups(config.Steps, &errors)
	validateExit(config, stepMap, &errors)
	validateAllowedTargets(config.AllowedTargets, &errors)
	validateStepModels(config, config.Steps, &errors)
	validateIngestReferences(config.Steps, stepMap, &errors)
	validateKnobInfoReferences(config.Steps, config.Knobs, &errors)
	validateForwardReferences(config.Steps, stepOrder, &errors)
	validateKnobs(config.Knobs, &errors)
	validateGroups(config.Steps, &errors)
	validateRecursion(config.Steps, config.Knobs, &errors)
	validateGates(config.Steps, &errors)
	validateBudgetLimits(config.Knobs, &errors)
	validateCloneReferences(config.Steps, stepMap, &errors)
	validateSkipFirstSerial(config.Steps, &errors)
	validateAccumulateUsage(config.Steps, &errors)
	validatePrunedNodesFrom(config.Steps, stepMap, &errors)

	// Phase 1 LADR grammar validators.
	validateGrammarVersion(config, &errors)
	validatePhase1(config, stepMap, &errors)

	return ValidationResult{Valid: len(errors) == 0, Errors: errors}
}

// ---------------------------------------------------------------------------
// 0. validateGrammarVersion — ladrVersion must match a supported version
// ---------------------------------------------------------------------------

func validateGrammarVersion(config *PipelineConfig, errors *[]string) {
	if config.LadrVersion == "" {
		return // absent → defaults to current (backward compat)
	}
	if config.LadrVersion != CurrentGrammarVersion {
		*errors = append(*errors,
			`ladrVersion "`+config.LadrVersion+
				`" is not supported by this engine (supports "`+
				CurrentGrammarVersion+`")`)
	}
}

// ---------------------------------------------------------------------------
// helpers: step maps
// ---------------------------------------------------------------------------

// stepEntry tracks both the step and (for group children) the parent group id.
type stepEntry struct {
	step   Step
	parent string // parent group id, "" for top-level
}

func buildStepMap(steps []Step) map[string]Step {
	m := make(map[string]Step)
	for _, step := range steps {
		m[step.ID()] = step
		if step.Kind == StepTypeGroup {
			for _, child := range step.Group.Steps {
				m[child.ID()] = child
			}
		}
	}
	return m
}

func buildStepOrder(steps []Step) map[string]int {
	order := make(map[string]int)
	idx := 0
	for _, step := range steps {
		if step.Kind == StepTypeGroup {
			for _, child := range step.Group.Steps {
				order[child.ID()] = idx
			}
		} else {
			order[step.ID()] = idx
		}
		idx++
	}
	return order
}

func buildGroupMap(steps []Step) map[string]string {
	m := make(map[string]string)
	for _, step := range steps {
		if step.Kind == StepTypeGroup {
			for _, child := range step.Group.Steps {
				m[child.ID()] = step.Group.ID
			}
		}
	}
	return m
}

// flattenNonGroupSteps collects all Normal and Sequential steps, descending one
// level into groups (which cannot themselves contain groups).
func flattenNonGroupSteps(steps []Step) []*BaseStep {
	var result []*BaseStep
	for i := range steps {
		s := &steps[i]
		if s.Kind == StepTypeGroup {
			for j := range s.Group.Steps {
				c := &s.Group.Steps[j]
				if c.Kind != StepTypeGroup {
					result = append(result, c.Base())
				}
			}
		} else {
			result = append(result, s.Base())
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// 1. validateStepUniqueness
// ---------------------------------------------------------------------------

// validateStepModels checks per-step `model:` overrides against the config's
// allowedTargets — a constrained ladder can't smuggle a disallowed model in
// at the step level.
func validateStepModels(config *PipelineConfig, steps []Step, errors *[]string) {
	if config.AllowedTargets.Strategy != AllowedTargetsConstrained {
		return
	}
	allowed := map[string]bool{}
	for _, m := range config.AllowedTargets.Models {
		allowed[m] = true
	}
	var walk func(step Step)
	walk = func(step Step) {
		if base := step.Base(); base != nil && base.Model != "" {
			if !allowed[base.Model] {
				*errors = append(*errors,
					fmt.Sprintf("Step %q: model %q is not in allowedTargets.models", step.ID(), base.Model))
			}
		}
		if step.Group != nil {
			for _, child := range step.Group.Steps {
				walk(child)
			}
		}
	}
	for _, step := range steps {
		walk(step)
	}
}

func validateStepUniqueness(steps []Step, errors *[]string) {
	var ids []string
	collectIDs(steps, &ids)
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			*errors = append(*errors, `Duplicate step id: "`+id+`"`)
		}
		seen[id] = true
	}
}

func collectIDs(steps []Step, ids *[]string) {
	for _, step := range steps {
		*ids = append(*ids, step.ID())
		if step.Kind == StepTypeGroup {
			collectIDs(step.Group.Steps, ids)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. validateNoNestedGroups
// ---------------------------------------------------------------------------

func validateNoNestedGroups(steps []Step, errors *[]string) {
	for _, step := range steps {
		if step.Kind != StepTypeGroup {
			continue
		}
		for _, child := range step.Group.Steps {
			if child.Kind == StepTypeGroup {
				*errors = append(*errors,
					`Nested group detected: group "`+child.ID()+
						`" inside group "`+step.Group.ID+`"`)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 3. validateExit
// ---------------------------------------------------------------------------

func validateExit(config *PipelineConfig, stepMap map[string]Step, errors *[]string) {
	exitStep, ok := stepMap[config.Exit]
	if !ok {
		*errors = append(*errors,
			`Exit references non-existent step id: "`+config.Exit+`"`)
		return
	}

	// Exit step can only have nodes if it is sequential.
	if exitStep.Kind != StepTypeGroup && exitStep.Kind != StepTypeSequential {
		base := exitStep.Base()
		if base != nil && base.Nodes != nil {
			*errors = append(*errors,
				`Exit step "`+config.Exit+
					`" can only have nodes if it is a sequential step (normal steps with nodes run in parallel, so there is no deterministic last node)`)
		}
	}

	// Validate init timeline steps.
	nonGroupSteps := flattenNonGroupSteps(config.Steps)
	var initSteps []*BaseStep
	for _, s := range nonGroupSteps {
		if s.Timeline != nil && *s.Timeline == TimelineInit {
			initSteps = append(initSteps, s)
		}
	}
	if len(initSteps) > 1 {
		*errors = append(*errors,
			`At most one step can have timeline: "init" (found `+itoa(len(initSteps))+`)`)
	}
	if len(initSteps) == 1 {
		initStep := initSteps[0]
		if initStep.Nodes != nil {
			*errors = append(*errors,
				`Init step "`+initStep.ID+`" cannot have nodes (must be a single-output step)`)
		}
		if initStep.ID == config.Exit {
			*errors = append(*errors,
				`Init step "`+initStep.ID+`" cannot be the same as exit step`)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. validateAllowedTargets
// ---------------------------------------------------------------------------

func validateAllowedTargets(policy AllowedTargetsPolicy, errors *[]string) {
	if policy.Strategy != AllowedTargetsUniversal && policy.Strategy != AllowedTargetsConstrained {
		*errors = append(*errors,
			`allowedTargets.strategy must be "universal" or "constrained"`)
		return
	}
	if policy.Strategy == AllowedTargetsUniversal {
		return
	}

	providers := policy.Providers
	models := policy.Models

	if len(providers) == 0 {
		*errors = append(*errors,
			`allowedTargets.providers must include at least one entry for strategy "constrained"`)
	}
	if len(models) == 0 {
		*errors = append(*errors,
			`allowedTargets.models must include at least one entry for strategy "constrained"`)
	}

	// Validate individual entries are non-empty strings
	for _, p := range providers {
		if strings.TrimSpace(p) == "" {
			*errors = append(*errors,
				`allowedTargets.providers entries must be non-empty strings`)
			break
		}
	}
	for _, m := range models {
		if strings.TrimSpace(m) == "" {
			*errors = append(*errors,
				`allowedTargets.models entries must be non-empty strings`)
			break
		}
	}

	hasProviderWildcard := contains(providers, "*")
	if hasProviderWildcard && len(providers) > 1 {
		*errors = append(*errors,
			`allowedTargets.providers cannot combine "*" with explicit providers`)
	}
	hasModelWildcard := contains(models, "*")
	if hasModelWildcard && len(models) > 1 {
		*errors = append(*errors,
			`allowedTargets.models cannot combine "*" with explicit models`)
	}
}

// ---------------------------------------------------------------------------
// 5. validateIngestReferences
// ---------------------------------------------------------------------------

func validateIngestReferences(steps []Step, stepMap map[string]Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.Fields == nil || step.Fields.IsString {
			continue
		}
		for _, field := range step.Fields.Fields {
			switch field.Kind {
			case FieldIngest:
				if _, ok := stepMap[field.Ingest.From.StepID]; !ok {
					*errors = append(*errors,
						`Step "`+step.ID+`": field "`+field.Ingest.Name+
							`" references non-existent step "`+field.Ingest.From.StepID+`"`)
				}
			case FieldMultiIngest:
				for _, source := range field.MultiIngest.From {
					if _, ok := stepMap[source.StepID]; !ok {
						*errors = append(*errors,
							`Step "`+step.ID+`": field "`+field.MultiIngest.Name+
								`" references non-existent step "`+source.StepID+`"`)
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 6. validateKnobInfoReferences
// ---------------------------------------------------------------------------

func validateKnobInfoReferences(steps []Step, knobs map[string]Knob, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.Fields == nil || step.Fields.IsString {
			continue
		}
		for _, field := range step.Fields.Fields {
			if field.Kind != FieldKnobInfo {
				continue
			}
			if _, ok := knobs[field.KnobInfo.From]; !ok {
				*errors = append(*errors,
					`Step "`+step.ID+`": knobInfo field "`+field.KnobInfo.Name+
						`" references non-existent knob "`+field.KnobInfo.From+`"`)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 7. validateForwardReferences
// ---------------------------------------------------------------------------

func validateForwardReferences(steps []Step, stepOrder map[string]int, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	groupMap := buildGroupMap(steps)

	for _, step := range nonGroupSteps {
		if step.Fields == nil || step.Fields.IsString {
			continue
		}
		stepIdx, hasIdx := stepOrder[step.ID]
		if !hasIdx {
			continue
		}

		myGroup := groupMap[step.ID]

		for _, field := range step.Fields.Fields {
			if field.Kind != FieldIngest && field.Kind != FieldMultiIngest {
				continue
			}

			var sources []FieldSourceRef
			if field.Kind == FieldIngest {
				sources = []FieldSourceRef{field.Ingest.From}
			} else {
				sources = field.MultiIngest.From
			}

			for _, source := range sources {
				if source.LoopRef.Kind != LoopRefString || source.LoopRef.Str != "current" {
					continue
				}

				refStepIdx, hasRef := stepOrder[source.StepID]
				if !hasRef {
					continue
				}

				if source.StepID == step.ID {
					if step.Type != StepTypeSequential {
						*errors = append(*errors,
							`Step "`+step.ID+`": non-sequential step cannot self-reference with loopRef: "current"`)
					}
					continue
				}

				refGroup := groupMap[source.StepID]

				if refStepIdx > stepIdx {
					*errors = append(*errors,
						`Step "`+step.ID+`": field "`+fieldName(field)+
							`" forward-references step "`+source.StepID+
							`" with loopRef: "current" (referenced step hasn't executed yet)`)
					continue
				}

				if refStepIdx == stepIdx {
					if myGroup != "" && refGroup != "" && myGroup == refGroup {
						*errors = append(*errors,
							`Step "`+step.ID+`": field "`+fieldName(field)+
								`" references step "`+source.StepID+
								`" in the same group with loopRef: "current" (parallel steps can't ingest from each other)`)
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 8. validateKnobs
// ---------------------------------------------------------------------------

func validateKnobs(knobs map[string]Knob, errors *[]string) {
	knobTypes := map[KnobType]int{
		KnobTypeLoops:     0,
		KnobTypeRecursion: 0,
		KnobTypeNodes:     0,
		KnobTypeGeneric:   0,
	}

	for key, knob := range knobs {
		switch knob.Type {
		case KnobTypeLoops, KnobTypeRecursion, KnobTypeNodes, KnobTypeGeneric:
			knobTypes[knob.Type]++
		default:
			*errors = append(*errors,
				`Knob "`+key+`": invalid type "`+string(knob.Type)+`"`)
			continue
		}

		if knob.Input.Kind == KnobInputSlider {
			stepCount := len(knob.Input.Slider.Steps)
			if stepCount < 3 || stepCount > 5 {
				*errors = append(*errors,
					`Knob "`+key+`": slider must have 3-5 steps (found `+itoa(stepCount)+`)`)
			}
			var defaults int
			for _, s := range knob.Input.Slider.Steps {
				if s.Default {
					defaults++
				}
			}
			if defaults == 0 {
				*errors = append(*errors,
					`Knob "`+key+`": slider must have exactly one step with default: true (found none)`)
			}
			if defaults > 1 {
				*errors = append(*errors,
					`Knob "`+key+`": slider must have exactly one step with default: true (found `+itoa(defaults)+`)`)
			}
		}

		if knob.Input.Kind == KnobInputNumerical {
			min := knob.Input.Numerical.Min
			var def float64
			if knob.Input.Numerical.Default != nil {
				def = *knob.Input.Numerical.Default
			}
			max := knob.Input.Numerical.Max
			if min > def {
				*errors = append(*errors,
					`Knob "`+key+`": min (`+ftoa(min)+`) > default (`+ftoa(def)+`)`)
			}
			if def > max {
				*errors = append(*errors,
					`Knob "`+key+`": default (`+ftoa(def)+`) > max (`+ftoa(max)+`)`)
			}
		}
	}

	if knobTypes[KnobTypeLoops] > 1 {
		*errors = append(*errors,
			`At most one knob with type "loops" allowed (found `+itoa(knobTypes[KnobTypeLoops])+`)`)
	}
	if knobTypes[KnobTypeRecursion] > 1 {
		*errors = append(*errors,
			`At most one knob with type "recursion" allowed (found `+itoa(knobTypes[KnobTypeRecursion])+`)`)
	}
}

// ---------------------------------------------------------------------------
// 9. validateGroups
// ---------------------------------------------------------------------------

func validateGroups(steps []Step, errors *[]string) {
	for _, step := range steps {
		if step.Kind != StepTypeGroup {
			continue
		}
		if len(step.Group.Steps) < 2 {
			*errors = append(*errors,
				`Group "`+step.Group.ID+`" must have at least 2 steps (found `+itoa(len(step.Group.Steps))+`)`)
		}
	}
}

// ---------------------------------------------------------------------------
// 10. validateRecursion
// ---------------------------------------------------------------------------

var knobRefRegex = regexp.MustCompile(`^\{\{knobs\.(.+)\}\}$`)

func extractKnobReference(value string) (string, bool) {
	match := knobRefRegex.FindStringSubmatch(value)
	if match == nil {
		return "", false
	}
	return match[1], true
}

func validateRecursion(steps []Step, knobs map[string]Knob, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	recursionCount := 0

	for _, step := range nonGroupSteps {
		if step.Nodes != nil && step.Nodes.Kind == NodesString {
			if ref, ok := extractKnobReference(step.Nodes.Str); ok {
				if _, exists := knobs[ref]; !exists {
					*errors = append(*errors,
						`Step "`+step.ID+`": nodes references non-existent knob "`+ref+`"`)
				}
			}
		}

		if step.Recursion != nil {
			recursionCount++
			if step.Recursion.MaxDepthNumOrStr.IsStr {
				if ref, ok := extractKnobReference(step.Recursion.MaxDepthNumOrStr.Str); ok {
					if _, exists := knobs[ref]; !exists {
						*errors = append(*errors,
							`Step "`+step.ID+`": recursion.maxDepth references non-existent knob "`+ref+`"`)
					}
				}
			}
		}
	}

	if recursionCount > 1 {
		*errors = append(*errors,
			`At most one step can have recursion (found `+itoa(recursionCount)+`)`)
	}
}

// ---------------------------------------------------------------------------
// 11. validateGates
// ---------------------------------------------------------------------------

func validateGates(steps []Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.ContinueIf == "" {
			continue
		}
		if strings.TrimSpace(step.ContinueIf) == "" {
			*errors = append(*errors,
				`Step "`+step.ID+`": continueIf must be a non-empty string`)
		}

		hasMultipleNodes := step.Nodes != nil && step.Type != StepTypeSequential
		if !hasMultipleNodes && step.Nodes == nil {
			*errors = append(*errors,
				`Step "`+step.ID+`": continueIf has no effect on a single-node step (no nodes defined)`)
		}
	}
}

// ---------------------------------------------------------------------------
// 12. validateBudgetLimits
// ---------------------------------------------------------------------------

func validateBudgetLimits(knobs map[string]Knob, errors *[]string) {
	const maxReasonableLoops = 50
	const maxReasonableNodes = 20
	const maxReasonableRecursion = 5

	for key, knob := range knobs {
		switch knob.Type {
		case KnobTypeLoops:
			val := knobDefaultOrMax(knob)
			if val > maxReasonableLoops {
				*errors = append(*errors,
					`Knob "`+key+`": loop count `+ftoa(val)+` exceeds maximum (`+itoa(maxReasonableLoops)+`). Lower the default or set ENGINE_MAX_LOOPS.`)
			}
		case KnobTypeNodes:
			val := knobDefaultOrMax(knob)
			if val > maxReasonableNodes {
				*errors = append(*errors,
					`Knob "`+key+`": node count `+ftoa(val)+` exceeds maximum (`+itoa(maxReasonableNodes)+`). Lower the default or set ENGINE_MAX_NODES.`)
			}
		case KnobTypeRecursion:
			val := knobDefaultOrMax(knob)
			if val > maxReasonableRecursion {
				*errors = append(*errors,
					`Knob "`+key+`": recursion depth `+ftoa(val)+` exceeds maximum (`+itoa(maxReasonableRecursion)+`). Lower the default or set ENGINE_MAX_RECURSION.`)
			}
		}
	}
}

// knobDefaultOrMax returns the default value for a knob, or its max, or 0.
// Matches TS: Number(knob.default ?? knob.max ?? 0) — ?? is null/undefined check.
func knobDefaultOrMax(knob Knob) float64 {
	if knob.Input.Kind == KnobInputNumerical {
		if knob.Input.Numerical.Default != nil {
			return *knob.Input.Numerical.Default
		}
		if knob.Input.Numerical.Max != 0 {
			return knob.Input.Numerical.Max
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// 13. validateCloneReferences
// ---------------------------------------------------------------------------

var cloneRegex = regexp.MustCompile(`^clone:(.+)$`)

func validateCloneReferences(steps []Step, stepMap map[string]Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.Fields == nil || !step.Fields.IsString {
			continue
		}
		match := cloneRegex.FindStringSubmatch(step.Fields.Str)
		if match == nil {
			continue
		}
		refID := match[1]
		refStep, ok := stepMap[refID]
		if !ok {
			*errors = append(*errors,
				`Step "`+step.ID+`": clone references non-existent step "`+refID+`"`)
		} else if refStep.Kind == StepTypeGroup {
			*errors = append(*errors,
				`Step "`+step.ID+`": clone references group "`+refID+`" (groups have no fields to clone)`)
		}
	}
}

// ---------------------------------------------------------------------------
// 14. validateSkipFirstSerial
// ---------------------------------------------------------------------------

func validateSkipFirstSerial(steps []Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.Fields == nil || step.Fields.IsString {
			continue
		}
		for _, field := range step.Fields.Fields {
			if field.Kind == FieldIngest && field.Ingest.SkipFirstNode && step.Type != StepTypeSequential {
				*errors = append(*errors,
					`Step "`+step.ID+`": field "`+field.Ingest.Name+
						`" has skipFirstNode: true but step type is "`+string(step.Type)+
						`" (only valid on sequential steps)`)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 15. validateAccumulateUsage
// ---------------------------------------------------------------------------

func validateAccumulateUsage(steps []Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.Fields == nil || step.Fields.IsString {
			continue
		}
		for _, field := range step.Fields.Fields {
			if field.Kind != FieldIngest {
				continue
			}
			if field.Ingest.From.LoopRef.Kind == LoopRefString && field.Ingest.From.LoopRef.Str == "accumulate" {
				*errors = append(*errors,
					`Step "`+step.ID+`": ingest field "`+field.Ingest.Name+
						`" cannot use loopRef: "accumulate" (use multi_ingest instead)`)
			}
			if field.Ingest.From.NodeRef == NodeRefAccumulate {
				*errors = append(*errors,
					`Step "`+step.ID+`": ingest field "`+field.Ingest.Name+
						`" cannot use nodeRef: "accumulate" (use multi_ingest instead)`)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 16. validatePrunedNodesFrom
// ---------------------------------------------------------------------------

func validatePrunedNodesFrom(steps []Step, stepMap map[string]Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.Nodes == nil || step.Nodes.Kind != NodesFromObj {
			continue
		}
		if !step.Nodes.From.Pruned {
			continue
		}

		source, ok := stepMap[step.Nodes.From.StepID]
		if !ok || source.Kind == StepTypeGroup {
			continue
		}
		sourceBase := source.Base()
		if sourceBase == nil || sourceBase.ContinueIf == "" {
			*errors = append(*errors,
				`Step "`+step.ID+`": nodes.from.pruned requires source step "`+
					step.Nodes.From.StepID+`" to define continueIf`)
		}
	}
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func fieldName(f Field) string {
	switch f.Kind {
	case FieldText:
		return f.Text.Name
	case FieldIngest:
		return f.Ingest.Name
	case FieldMultiIngest:
		return f.MultiIngest.Name
	case FieldNodeInfo:
		return f.NodeInfo.Name
	case FieldKnobInfo:
		return f.KnobInfo.Name
	}
	return ""
}

func contains(slice []string, v string) bool {
	for _, s := range slice {
		if s == v {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
