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

// Package engine — field and node-count resolution.
// Ported from packages/engine-core/resolution.ts.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/ladr/ladr/internal/indexer"
	"github.com/ladr/ladr/internal/ladr"
)

// IsStep returns true if the step is NOT a group step (i.e., it's normal or
// sequential — an executable step).
func IsStep(s ladr.Step) bool {
	return s.Kind != ladr.StepTypeGroup
}

// ExtractFields resolves a step's fields, following clone: references.
func ExtractFields(step ladr.Step, allSteps map[string]ladr.Step) []ladr.Field {
	base := step.Base()
	if base == nil || base.Fields == nil {
		return nil
	}
	fc := *base.Fields
	if !fc.IsString {
		return fc.Fields
	}
	// clone: reference
	match := cloneRefRe.FindStringSubmatch(fc.Str)
	if match == nil {
		return nil
	}
	source, ok := allSteps[match[1]]
	if !ok {
		return nil
	}
	srcBase := source.Base()
	if srcBase == nil || srcBase.Fields == nil {
		return nil
	}
	srcFC := *srcBase.Fields
	if srcFC.IsString {
		return nil
	}
	return srcFC.Fields
}

var cloneRefRe = regexp.MustCompile(`^clone:(.+)$`)

// ParseKnobReference extracts the knob name from "{{knobs.X}}", or returns
// ("", false) if the string doesn't match.
func ParseKnobReference(raw string) (string, bool) {
	m := knobRefRe.FindStringSubmatch(raw)
	if m == nil {
		return "", false
	}
	return m[1], true
}

var knobRefRe = regexp.MustCompile(`^{{knobs\.(.+)}}$`)

// ParseStepOutputReference extracts the step ID from "{{X.output}}", or returns
// ("", false) if the string doesn't match.
func ParseStepOutputReference(raw string) (string, bool) {
	m := stepOutputRe.FindStringSubmatch(raw)
	if m == nil {
		return "", false
	}
	return m[1], true
}

var stepOutputRe = regexp.MustCompile(`^{{(.+)\.output}}$`)

// GetPrunedNodeMapKey builds the map key for pruned-node lookups.
func GetPrunedNodeMapKey(loopIndex int, stepID string) string {
	return fmt.Sprintf("%d:%s", loopIndex, stepID)
}

// RemapNodeThroughPrunedState translates a logical node number into the
// surviving physical node number, or returns nil if there's no survivor for
// that position.
func RemapNodeThroughPrunedState(runtime *ExecutionRuntimeContext, stepID string, loopIndex int, nodeNumber *int) *int {
	if nodeNumber == nil {
		return nil
	}
	if runtime.PrunedNodeMap == nil {
		return nodeNumber
	}
	key := GetPrunedNodeMapKey(loopIndex, stepID)
	pruned, ok := runtime.PrunedNodeMap.Get(key)
	if !ok {
		return nodeNumber
	}
	if len(pruned) == 0 {
		return nil
	}
	idx := *nodeNumber - 1
	if idx < 0 || idx >= len(pruned) {
		return nil
	}
	v := pruned[idx]
	return &v
}

// strictIntResult holds the outcome of ParseStrictIntegerCount.
type strictIntResult struct {
	Value      *int
	Normalized string
	Reason     string
}

// ReasonOrOK returns the reason, defaulting to "ok" when empty (matching TS behavior).
func (s strictIntResult) ReasonOrOK() string {
	if s.Reason == "" {
		return "ok"
	}
	return s.Reason
}

// ParseStrictIntegerCount attempts to parse a string as a non-negative integer,
// tolerating trailing ".0…" decimals.
func ParseStrictIntegerCount(raw *string) strictIntResult {
	normalized := ""
	if raw != nil {
		normalized = strings.TrimSpace(*raw)
	}
	if normalized == "" {
		return strictIntResult{Value: nil, Normalized: normalized, Reason: "empty_output"}
	}
	m := strictIntRe.FindStringSubmatch(normalized)
	if m == nil {
		return strictIntResult{Value: nil, Normalized: normalized, Reason: "invalid_numeric_format"}
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return strictIntResult{Value: nil, Normalized: normalized, Reason: "invalid_numeric_format"}
	}
	return strictIntResult{Value: &n, Normalized: normalized}
}

var strictIntRe = regexp.MustCompile(`^(\d+)(?:\.(?:0*)?)?$`)

// resolveLoopIndex resolves a LoopRef to a concrete loop index, or returns
// (-1, false) for "accumulate" (caller handles) or negative indices.
func resolveLoopIndex(ref ladr.LoopRef, runtimeLoopIndex int) (int, bool) {
	switch ref.Kind {
	case ladr.LoopRefString:
		switch ref.Str {
		case "accumulate":
			return -1, false
		case "current":
			return runtimeLoopIndex, true
		case "previous":
			return runtimeLoopIndex - 1, true
		}
	case ladr.LoopRefNumber:
		return int(ref.Num), true
	}
	return -1, false
}

// ResolveLoopIndexes expands a LoopRef into a list of concrete loop indexes.
// "accumulate" yields [0, 1, … runtimeLoopIndex-1].
func ResolveLoopIndexes(ref ladr.LoopRef, runtimeLoopIndex int) []int {
	if ref.Kind == ladr.LoopRefString && ref.Str == "accumulate" {
		indexes := make([]int, runtimeLoopIndex)
		for i := range indexes {
			indexes[i] = i
		}
		return indexes
	}
	resolved, ok := resolveLoopIndex(ref, runtimeLoopIndex)
	if !ok || resolved < 0 {
		return nil
	}
	return []int{resolved}
}

// countStepNodes queries the IO layer for how many nodes a step produced.
func countStepNodes(ctx context.Context, io EngineIO, instanceID string, loopIndex int, stepID, groupID string) (int, error) {
	return io.GetNodeCount(ctx, GetNodeCountArgs{
		InstanceID: instanceID,
		LoopIndex:  loopIndex,
		StepID:     stepID,
		GroupID:    groupID,
	})
}

// ResolveIngestValues reads all output values referenced by a single ingest
// source spec.
func ResolveIngestValues(
	ctx context.Context,
	source ladr.FieldSourceRef,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
) ([]string, error) {
	loopIndexes := ResolveLoopIndexes(source.LoopRef, runtime.LoopIndex)
	if len(loopIndexes) == 0 {
		return nil, nil
	}

	// --- accumulate nodeRef ---
	if source.NodeRef == ladr.NodeRefAccumulate {
		var allRefs []SourceRef
		for _, loopIdx := range loopIndexes {
			count, err := io.GetNodeCount(ctx, GetNodeCountArgs{
				InstanceID: runtime.InstanceID,
				LoopIndex:  loopIdx,
				StepID:     source.StepID,
			})
			if err != nil {
				return nil, err
			}
			for n := 1; n <= count; n++ {
				nodeNum := n
				allRefs = append(allRefs, SourceRef{
					LoopIndex:  loopIdx,
					StepID:     source.StepID,
					NodeNumber: &nodeNum,
				})
			}
		}
		if len(allRefs) == 0 {
			return nil, nil
		}
		return io.ReadOutputArray(ctx, ReadOutputArrayArgs{
			InstanceID:  runtime.InstanceID,
			FromSources: allRefs,
		})
	}

	// --- current / previous / unspecified nodeRef ---
	var refs []SourceRef
	for _, loopIndex := range loopIndexes {
		var rawNodeNumber *int
		switch source.NodeRef {
		case ladr.NodeRefCurrent:
			rawNodeNumber = runtime.CurrentNodeNumber
		case ladr.NodeRefPrevious:
			if runtime.CurrentNodeNumber != nil {
				prev := max(1, *runtime.CurrentNodeNumber-1)
				rawNodeNumber = &prev
			} else {
				fallback := 1
				rawNodeNumber = &fallback
			}
		}
		nodeNumber := RemapNodeThroughPrunedState(runtime, source.StepID, loopIndex, rawNodeNumber)
		refs = append(refs, SourceRef{
			LoopIndex:  loopIndex,
			StepID:     source.StepID,
			NodeNumber: nodeNumber,
		})
	}

	// Filter: for current/previous we require a non-nil nodeNumber
	var filtered []SourceRef
	for _, ref := range refs {
		if source.NodeRef == ladr.NodeRefCurrent || source.NodeRef == ladr.NodeRefPrevious {
			if ref.NodeNumber != nil {
				filtered = append(filtered, ref)
			}
		} else {
			filtered = append(filtered, ref)
		}
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	return io.ReadOutputArray(ctx, ReadOutputArrayArgs{
		InstanceID:  runtime.InstanceID,
		FromSources: filtered,
	})
}

// ResolveIngestSource resolves a single IngestField to a string value.
func ResolveIngestSource(
	ctx context.Context,
	field ladr.IngestField,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
) (string, error) {
	if field.SkipFirstNode && runtime.CurrentNodeNumber != nil && *runtime.CurrentNodeNumber == 1 {
		return "", nil
	}
	values, err := ResolveIngestValues(ctx, field.From, runtime, io)
	if err != nil {
		return "", err
	}
	if (field.From.NodeRef == ladr.NodeRefCurrent || field.From.NodeRef == ladr.NodeRefPrevious) && len(values) == 0 {
		curNode := 0
		if runtime.CurrentNodeNumber != nil {
			curNode = *runtime.CurrentNodeNumber
		}
		return "", NewNodeDependencyError(fmt.Sprintf(
			`Missing required node output from step "%s" for node %d`,
			field.From.StepID, curNode,
		))
	}
	if len(values) > 0 {
		return values[0], nil
	}
	return "", nil
}

// normalizeText converts any value to a string, returning "" for nil.
func normalizeText(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		// Knobs are float64; integers should not have a trailing ".0".
		if val == math.Trunc(val) && !math.IsInf(val, 0) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case bool:
		return strconv.FormatBool(val)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ResolveField resolves a single field definition to a string or []string,
// dispatching by field type.
//
// Resolution priority (highest to lowest):
//  1. passField override — if runtime.PassFieldOverrides contains the field's name,
//     that value wins outright. Set by the caller's passFields on a jump.
//  2. Normal `from:` — declared primary source.
//  3. `fallback:` — declared secondary source, consulted when primary misses.
//
// For field types that support `fallback:` (TextField, IngestField, RelikReadField),
// the fallback FromSpec is consulted when the primary source misses — i.e. returns
// an empty value OR a *NodeDependencyError (source-missing). Other errors propagate.
func ResolveField(
	ctx context.Context,
	field ladr.Field,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
) (any, error) {
	// 1. passField override (highest priority). Applies to all field types —
	//    any field can be the target of a passField.to.
	if runtime.PassFieldOverrides != nil {
		fieldName := fieldDisplayName(field)
		if fieldName != "" {
			if override, ok := runtime.PassFieldOverrides[fieldName]; ok {
				return override, nil
			}
		}
	}

	switch field.Kind {
	case ladr.FieldText:
		primary := normalizeText(runtime.Inputs[field.Text.From])
		if primary != "" || field.Text.Fallback == nil {
			return primary, nil
		}
		return resolveFallback(ctx, *field.Text.Fallback, runtime, io)

	case ladr.FieldKnobInfo:
		if v, ok := runtime.Knobs[field.KnobInfo.From]; ok {
			return normalizeText(v), nil
		}
		return "", nil

	case ladr.FieldNodeInfo:
		if runtime.CurrentNodeNumber != nil {
			return strconv.Itoa(*runtime.CurrentNodeNumber), nil
		}
		return "", nil

	case ladr.FieldIngest:
		primary, err := ResolveIngestSource(ctx, field.Ingest, runtime, io)
		if err == nil && primary != "" {
			return primary, nil
		}
		if field.Ingest.Fallback == nil {
			return primary, err
		}
		// Only trigger fallback on empty-result or source-missing error. Other errors propagate.
		if err != nil {
			var nde *NodeDependencyError
			if !errors.As(err, &nde) {
				return primary, err
			}
		}
		return resolveFallback(ctx, *field.Ingest.Fallback, runtime, io)

	case ladr.FieldMultiIngest:
		var values []string
		for _, source := range field.MultiIngest.From {
			sourceValues, err := ResolveIngestValues(ctx, source, runtime, io)
			if err != nil {
				return nil, err
			}
			if (source.NodeRef == ladr.NodeRefCurrent || source.NodeRef == ladr.NodeRefPrevious) && len(sourceValues) == 0 {
				curNode := 0
				if runtime.CurrentNodeNumber != nil {
					curNode = *runtime.CurrentNodeNumber
				}
				return nil, NewNodeDependencyError(fmt.Sprintf(
					`Missing required node output from step "%s" for node %d`,
					source.StepID, curNode,
				))
			}
			for _, item := range sourceValues {
				if item != "" {
					values = append(values, item)
				}
			}
		}
		return values, nil

	case ladr.FieldRelikRead:
		primary, err := resolveRelikReadField(ctx, field.RelikRead, runtime)
		if err == nil {
			if s, _ := primary.(string); s != "" || field.RelikRead.Fallback == nil {
				return primary, nil
			}
		} else if field.RelikRead.Fallback == nil {
			return primary, err
		} else {
			// Only trigger fallback on source-missing error. Other errors propagate.
			var nde *NodeDependencyError
			if !errors.As(err, &nde) {
				return primary, err
			}
		}
		return resolveFallback(ctx, *field.RelikRead.Fallback, runtime, io)

	case ladr.FieldManifest:
		return resolveManifestField(ctx, field.Manifest, runtime, io)

	case ladr.FieldIndexSearch:
		return resolveIndexSearchField(ctx, field.IndexSearch, runtime)
	}

	return "", nil
}

// resolveFallback consults a fallback FromSpec when a field's primary source
// missed. The fallback uses the full unified source vocabulary (previous, output,
// {stepId}, {relik}, literal, input.* keys). Errors from the fallback propagate
// to the caller — there is no further fallback chain.
func resolveFallback(
	ctx context.Context,
	fallback ladr.FromSpec,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
) (any, error) {
	v, err := ResolveFromSpec(ctx, fallback, runtime, io, runtime.RelikStore, "")
	if err != nil {
		return "", err
	}
	return v, nil
}

// fieldDisplayName extracts the declared Name from any field variant. Used to
// look up passField overrides by field name. Returns "" for variants without
// a name (none today — all field variants have a Name).
func fieldDisplayName(field ladr.Field) string {
	switch field.Kind {
	case ladr.FieldText:
		return field.Text.Name
	case ladr.FieldIngest:
		return field.Ingest.Name
	case ladr.FieldMultiIngest:
		return field.MultiIngest.Name
	case ladr.FieldNodeInfo:
		return field.NodeInfo.Name
	case ladr.FieldKnobInfo:
		return field.KnobInfo.Name
	case ladr.FieldRelikRead:
		return field.RelikRead.Name
	case ladr.FieldManifest:
		return field.Manifest.Name
	case ladr.FieldIndexSearch:
		return field.IndexSearch.Name
	}
	return ""
}

// resolvePassFieldOverrides resolves each passField's From in the CALLER's
// runtime context and builds the {targetFieldName: resolvedValue} map that
// becomes the target step's PassFieldOverrides.
//
// Errors propagate — if any passField.From fails to resolve, the whole jump
// fails. Per the locked-in priority rule, passField is the highest-priority
// source; a half-resolved override map would silently degrade behavior, so
// we fail loud instead.
func resolvePassFieldOverrides(
	ctx context.Context,
	passFields []ladr.PassField,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
	currentStepOutput string,
) (map[string]string, error) {
	if len(passFields) == 0 {
		return nil, nil
	}
	overrides := make(map[string]string, len(passFields))
	for _, pf := range passFields {
		if pf.To == "" {
			continue
		}
		val, err := ResolveFromSpec(ctx, pf.From, runtime, io, runtime.RelikStore, currentStepOutput)
		if err != nil {
			return nil, fmt.Errorf("passField.to %q: resolve from: %w", pf.To, err)
		}
		overrides[pf.To] = val
	}
	return overrides, nil
}

// ResolveNodeCount resolves the number of nodes a step should execute.
// Handles literal numbers, knob references, step-output references, and
// object { from: NodesFrom } specs.
func ResolveNodeCount(
	ctx context.Context,
	nodes *ladr.Nodes,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
) (int, error) {
	if nodes == nil {
		return 1, nil
	}

	switch nodes.Kind {
	case ladr.NodesNumber:
		return max(1, int(math.Floor(nodes.Num))), nil

	case ladr.NodesString:
		// Try knob reference {{knobs.X}}
		if knobName, ok := ParseKnobReference(nodes.Str); ok {
			if val, ok := runtime.Knobs[knobName]; ok {
				if !math.IsNaN(val) && !math.IsInf(val, 0) {
					return max(1, int(math.Floor(val))), nil
				}
			}
			return 1, nil
		}

		// Try step-output reference {{X.output}}
		if stepRef, ok := ParseStepOutputReference(nodes.Str); ok {
			output, err := io.ReadOutput(ctx, ReadOutputArgs{
				InstanceID: runtime.InstanceID,
				LoopIndex:  runtime.LoopIndex,
				StepID:     stepRef,
			})
			if err != nil {
				return 0, err
			}
			parsed := ParseStrictIntegerCount(&output)
			value := 0
			if parsed.Value != nil {
				value = max(0, *parsed.Value)
			}
			_ = io.PublishEvent(ctx, PublishEventArgs{
				InstanceID: runtime.InstanceID,
				Type:       EventNodeCountResolved,
				Data: map[string]any{
					"loopIndex":        runtime.LoopIndex,
					"stepId":           runtime.CurrentStepID,
					"sourceStepId":     stepRef,
					"rawOutput":        output,
					"normalizedOutput": parsed.Normalized,
					"nodeCount":        value,
					"accepted":         parsed.Value != nil,
					"reason":           parsed.ReasonOrOK(),
				},
			})
			return value, nil
		}

	case ladr.NodesFromObj:
		return resolveNodeCountFrom(ctx, nodes.From, runtime, io)
	}

	return 1, nil
}

func resolveNodeCountFrom(
	ctx context.Context,
	from ladr.NodesFrom,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
) (int, error) {
	loopIndexes := ResolveLoopIndexes(from.LoopRef, runtime.LoopIndex)

	if from.Pruned {
		total := 0
		for _, loopIndex := range loopIndexes {
			key := GetPrunedNodeMapKey(loopIndex, from.StepID)
			if mapped, ok := runtime.PrunedNodeMap.Get(key); ok {
				total += len(mapped)
			} else {
				count, err := countStepNodes(ctx, io, runtime.InstanceID, loopIndex, from.StepID, "")
				if err != nil {
					return 0, err
				}
				total += count
			}
		}
		return max(0, total), nil
	}

	if len(loopIndexes) == 0 {
		return 1, nil
	}
	resolvedLoop := loopIndexes[0]

	var rawNodeNumber *int
	switch from.NodeRef {
	case ladr.NodeRefCurrent:
		rawNodeNumber = runtime.CurrentNodeNumber
	case ladr.NodeRefPrevious:
		if runtime.CurrentNodeNumber != nil {
			prev := max(1, *runtime.CurrentNodeNumber-1)
			rawNodeNumber = &prev
		} else {
			fallback := 1
			rawNodeNumber = &fallback
		}
	}

	nodeNumber := RemapNodeThroughPrunedState(runtime, from.StepID, resolvedLoop, rawNodeNumber)
	if (from.NodeRef == ladr.NodeRefCurrent || from.NodeRef == ladr.NodeRefPrevious) && nodeNumber == nil {
		_ = io.PublishEvent(ctx, PublishEventArgs{
			InstanceID: runtime.InstanceID,
			Type:       EventNodeCountResolved,
			Data: map[string]any{
				"loopIndex":        runtime.LoopIndex,
				"stepId":           runtime.CurrentStepID,
				"sourceStepId":     from.StepID,
				"rawOutput":        "",
				"normalizedOutput": "",
				"nodeCount":        0,
				"accepted":         false,
				"reason":           "missing_source_node",
			},
		})
		return 0, nil
	}

	output, err := io.ReadOutput(ctx, ReadOutputArgs{
		InstanceID: runtime.InstanceID,
		LoopIndex:  resolvedLoop,
		StepID:     from.StepID,
		NodeNumber: nodeNumber,
	})
	if err != nil {
		return 0, err
	}
	parsed := ParseStrictIntegerCount(&output)
	value := 0
	if parsed.Value != nil {
		value = max(0, *parsed.Value)
	}
	_ = io.PublishEvent(ctx, PublishEventArgs{
		InstanceID: runtime.InstanceID,
		Type:       EventNodeCountResolved,
		Data: map[string]any{
			"loopIndex":        runtime.LoopIndex,
			"stepId":           runtime.CurrentStepID,
			"sourceStepId":     from.StepID,
			"rawOutput":        output,
			"normalizedOutput": parsed.Normalized,
			"nodeCount":        value,
			"accepted":         parsed.Value != nil,
			"reason":           parsed.ReasonOrOK(),
		},
	})
	return value, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// resolveRelikReadField resolves a relikRead field by reading from the relik store.
func resolveRelikReadField(ctx context.Context, field ladr.RelikReadField, runtime *ExecutionRuntimeContext) (any, error) {
	if runtime.RelikStore == nil {
		return "", fmt.Errorf("relikRead: no relik store available in runtime context")
	}

	target := field.From
	store := runtime.RelikStore

	// System metatag: folder index.
	if target.Metatag == "index" && !target.HasFile() {
		idx, err := store.ReadFolderIndex(target.Relik, target.Folder)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(idx)
		return string(b), nil
	}

	// System metatag: folder tree.
	if target.Metatag == "tree" && !target.HasFile() {
		depth := 1
		if field.Depth != nil {
			depth = *field.Depth
		}
		tree, err := store.ReadFolderTree(target.Relik, target.Folder, depth)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(tree)
		return string(b), nil
	}

	// File-level read.
	if target.HasFile() {
		if target.HasMetatag() {
			// Read a metatag value on a specific file.
			val, err := store.ReadMetatag(target.Relik, target.Folder, target.File, target.Metatag)
			if err != nil {
				return "", err
			}
			if val == nil {
				return "", nil
			}
			if s, ok := val.(string); ok {
				return s, nil
			}
			b, _ := json.Marshal(val)
			return string(b), nil
		}
		// Read file contents.
		return store.ReadFile(target.Relik, target.Folder, target.File)
	}

	// Folder-level metatag read (no file specified).
	if target.HasMetatag() {
		entries, err := store.ReadMetatagAcrossFolder(target.Relik, target.Folder, target.Metatag)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(entries)
		return string(b), nil
	}

	return "", fmt.Errorf("relikRead: invalid target (must specify file or metatag)")
}

// resolveManifestField resolves a manifest field by building the requested view,
// the default bundle, or the full dump.
func resolveManifestField(
	ctx context.Context,
	field ladr.ManifestField,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
) (any, error) {
	config := runtime.Config
	if config == nil {
		return "", nil
	}

	var result any
	var err error

	if field.From != nil {
		result, err = BuildManifestView(ctx, *field.From, config, runtime, io)
	} else if len(config.DefaultManifest) > 0 {
		result, err = BuildManifestBundle(ctx, config.DefaultManifest, config, runtime, io)
	} else {
		result, err = BuildManifest(ctx, config, runtime, io, runtime.RelikStore, runtime.PrunedNodeMap)
	}

	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// resolveIndexSearchField runs grep-grade FTS over the owner's indexed
// chunks and returns the hits as formatted text. Empty result is a valid
// answer ("nothing indexed matches") — the model navigates from there.
func resolveIndexSearchField(ctx context.Context, f ladr.IndexSearchField, runtime *ExecutionRuntimeContext) (any, error) {
	owner := runtime.OwnerID
	if owner == "" {
		// No owner addressing (bare local run): scope to the instance itself.
		owner = runtime.InstanceID
	}
	hits, err := indexer.Search(ctx, owner, f.Query, f.Limit)
	if err != nil {
		return nil, fmt.Errorf("indexSearch: %w", err)
	}
	if len(hits) == 0 {
		return "(no indexed chunks match \"" + f.Query + "\")", nil
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[chunk %s · %d tokens · turn %s · %s]\n%s",
			h.Hash[:12], h.Tokens, h.TurnID, h.Kind, h.Snippet)
	}
	return b.String(), nil
}
