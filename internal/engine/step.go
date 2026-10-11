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

// Package engine — step execution.
// Ported from packages/engine-core/step.ts.
package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/mailbox"
	pkgerrors "github.com/ladr/ladr/pkg/errors"
	"golang.org/x/sync/errgroup"
)

// maxParallelNodes is the batch size for parallel node execution.
const maxParallelNodes = 20

// nodeResult captures the output of a single node execution.
type nodeResult struct {
	NodeNumber *int
	Output     string
}

// failedNode captures a node execution failure.
type failedNode struct {
	NodeNumber *int
	Message    string
}

// ResolveStepFields resolves all fields for a step in parallel using errgroup.
func ResolveStepFields(
	ctx context.Context,
	step ladr.Step,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
	stepsMap map[string]ladr.Step,
) ([]ResolvedField, error) {
	fields := ExtractFields(step, stepsMap)

	results := make([]ResolvedField, len(fields))
	g, ctx2 := errgroup.WithContext(ctx)
	for i, field := range fields {
		i, field := i, field
		g.Go(func() error {
			value, err := ResolveField(ctx2, field, runtime, io)
			if err != nil {
				return err
			}
			results[i] = ResolvedField{Name: field.Text.Name, Value: value}
			// Use the field name from the correct sub-struct
			switch field.Kind {
			case ladr.FieldText:
				results[i].Name = field.Text.Name
			case ladr.FieldIngest:
				results[i].Name = field.Ingest.Name
			case ladr.FieldMultiIngest:
				results[i].Name = field.MultiIngest.Name
			case ladr.FieldNodeInfo:
				results[i].Name = field.NodeInfo.Name
			case ladr.FieldKnobInfo:
				results[i].Name = field.KnobInfo.Name
			case ladr.FieldRelikRead:
				results[i].Name = field.RelikRead.Name
			case ladr.FieldManifest:
				results[i].Name = field.Manifest.Name
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// executeNode runs a single node: resolve fields → assemble prompt → LLM call →
// write output.  Returns the node output.
func executeNode(
	ctx context.Context,
	step ladr.Step,
	nodeNumber *int,
	runtimeBase *ExecutionRuntimeContext,
	io EngineIO,
	stepsMap map[string]ladr.Step,
	provider ProviderConfig,
	fallbackProviders []ProviderConfig,
	budget ResourceBudget,
	llmCallCount *int64,
) (string, error) {
	// Clone runtime with this node number
	runtime := *runtimeBase
	runtime.CurrentNodeNumber = nodeNumber

	resolvedFields, err := ResolveStepFields(ctx, step, &runtime, io, stepsMap)
	if err != nil {
		return "", err
	}

	base := step.Base()
	systemPrompt := ""
	if base != nil {
		systemPrompt = base.SystemPrompt
	}
	groupID := runtime.CurrentGroupID

	// Per-step model: a step declaring model: overrides the run's model for
	// its own LLM calls (copied config — the run's provider is untouched for
	// other steps). Spend is charged against the model actually used because
	// the cost lookup below reads this same variable.
	if base != nil && base.Model != "" && base.Model != provider.Model {
		provider = ProviderConfig{
			Provider: provider.Provider,
			Model:    base.Model,
			APIKey:   provider.APIKey,
			BaseURL:  provider.BaseURL,
		}
		fallbackProviders = nil // a step-scoped model pin has no fallback chain
	}

	// Per-step sampling overrides: thinking models need explicit maxTokens
	// (long reasoning chains truncate to nothing) and stages want distinct
	// temperatures (hot fan-outs, cold arbitration). Applied after the model
	// override so a step-scoped model pin keeps step-scoped sampling.
	if base != nil {
		if base.Temperature != nil {
			provider.Temperature = *base.Temperature
		}
		if base.MaxTokens != nil {
			provider.MaxTokens = *base.MaxTokens
		}
		if base.TimeoutMs != nil {
			provider.TimeoutMs = *base.TimeoutMs
		}
	}

	// makeLLMCall is the single counted, ceiling-checked LLM-call seam, shared by
	// the plain single-call path, the tools loop, and the retrieve loop. Central
	// budget control here so a loop's many calls each count against MaxTotalLlmCalls
	// and the spend ceiling (a loop node is no longer miscounted as one call).
	makeLLMCall := func(ctx context.Context, userPrompt string) (string, error) {
		if atomic.LoadInt64(llmCallCount) >= int64(budget.MaxTotalLlmCalls) {
			return "", pkgerrors.NewResourceLimitError(pkgerrors.LimitTotalCalls, budget.MaxTotalLlmCalls)
		}
		atomic.AddInt64(llmCallCount, 1)
		if runtimeBase.CeilingTracker != nil {
			if err := runtimeBase.CeilingTracker.IncLlmCall(); err != nil {
				return "", err
			}
		}
		var usage CallUsage
		result, err := io.ExecuteLlmCall(ctx, ExecuteLlmParams{
			InstanceID:        runtime.InstanceID,
			StepID:            step.ID(),
			GroupID:           groupID,
			LoopIndex:         runtime.LoopIndex,
			NodeNumber:        nodeNumber,
			UserPrompt:        userPrompt,
			Provider:          provider,
			FallbackProviders: fallbackProviders,
			Depth:             runtime.Depth,
			UsageOut:          &usage,
		})
		if err != nil {
			return result, err
		}
		// Track dollar spend if we have pricing data for this model.
		if runtimeBase.CeilingTracker != nil {
			if cost, found := CalculateCallCost(provider.Model, &usage); found && cost > 0 {
				if err := runtimeBase.CeilingTracker.AddSpend(cost); err != nil {
					return result, err
				}
			}
		}
		return result, nil
	}

	var output string
	switch {
	case base != nil && base.HasAdaptiveKeys():
		// Flat adaptive keys (codeExecution:, relikRead:, relikEdit:, files:) —
		// the bounded adaptive loop IS the dispatch. Handles all four tools
		// via shared requestMatch dispatch.
		output, err = ExecuteAdaptiveLoop(ctx, base.CodeExecution, base.RelikRead, base.RelikEdit, base.Files, base.ConfigEdit,
			resolvedFields, systemPrompt,
			io, runtimeBase.RelikStore, &runtime, step.ID(), groupID, nodeNumber, makeLLMCall)
	default:
		output, err = makeLLMCall(ctx, AssemblePrompt(resolvedFields, systemPrompt))
	}
	if err != nil {
		return "", err
	}

	if err := io.WriteOutput(ctx, WriteOutputArgs{
		InstanceID: runtime.InstanceID,
		LoopIndex:  runtime.LoopIndex,
		GroupID:    groupID,
		StepID:     step.ID(),
		NodeNumber: nodeNumber,
		Output:     output,
	}); err != nil {
		return "", err
	}

	return output, nil
}

// ExecuteSingleStep resolves node count, executes nodes (parallel batches or
// sequential), handles continueIf gate, prunes survivors, publishes events.
func ExecuteSingleStep(
	ctx context.Context,
	step ladr.Step,
	runtimeBase *ExecutionRuntimeContext,
	io EngineIO,
	stepsMap map[string]ladr.Step,
	provider ProviderConfig,
	fallbackProviders []ProviderConfig,
	budget ResourceBudget,
	llmCallCount *int64,
) (string, error) {
	base := step.Base()
	if base == nil {
		return "", fmt.Errorf("step %q has no base", step.ID())
	}

	rawNodeCount, err := ResolveNodeCount(ctx, base.Nodes, runtimeBase, io)
	if err != nil {
		return "", err
	}
	nodeCount := minInt(rawNodeCount, budget.MaxNodesPerStep)
	if nodeCount < rawNodeCount {
		_ = io.PublishEvent(ctx, PublishEventArgs{
			InstanceID: runtimeBase.InstanceID,
			Type:       EventNodeCountResolved,
			Data: map[string]any{
				"stepId":    step.ID(),
				"requested": rawNodeCount,
				"clamped":   nodeCount,
				"limit":     budget.MaxNodesPerStep,
			},
		})
	}

	var (
		outputs     []string
		nodeResults []nodeResult
		failedNodes []failedNode
		resultsMu   sync.Mutex
	)

	// Dynamic step: load config from the declared from: source before executing
	// any nodes. The loaded systemPrompt/fields/if replace the step's declared
	// (empty) values. On load failure, fire onInvalid or return an error.
	if base.Dynamic {
		manifest, _ := BuildManifest(ctx, runtimeBase.Config, runtimeBase, io, runtimeBase.RelikStore, runtimeBase.PrunedNodeMap)
		dynCfg, derr := LoadDynamicStep(ctx, base, runtimeBase, io, runtimeBase.RelikStore, manifest)
		if derr != nil {
			if base.OnInvalid != nil {
				return "", &dynamicLoadError{action: *base.OnInvalid, stepID: step.ID()}
			}
			return "", fmt.Errorf("step %q: %w", step.ID(), derr)
		}
		// Apply the loaded config to the step's base (in place — base is a pointer).
		base.SystemPrompt = dynCfg.SystemPrompt
		if len(dynCfg.Fields) > 0 {
			base.Fields = &ladr.FieldsConfig{Fields: dynCfg.Fields}
		}
		if dynCfg.If != nil {
			base.If = dynCfg.If
		}
		// Apply adaptive keys from the loaded config (all four — files:
		// was previously dropped here, so a dynamic step declaring it ran
		// without the tool).
		if dynCfg.CodeExecution != nil {
			base.CodeExecution = dynCfg.CodeExecution
		}
		if dynCfg.RelikRead != nil {
			base.RelikRead = dynCfg.RelikRead
		}
		if dynCfg.RelikEdit != nil {
			base.RelikEdit = dynCfg.RelikEdit
		}
		if dynCfg.Files != nil {
			base.Files = dynCfg.Files
		}
		if dynCfg.RelikWrite != nil {
			base.RelikWrite = dynCfg.RelikWrite
		}
		if dynCfg.Recursion != nil {
			base.Recursion = dynCfg.Recursion
		}
		if dynCfg.Model != "" {
			base.Model = dynCfg.Model
		}
		if dynCfg.Nodes != nil {
			// Re-resolve node count from the loaded config.
			base.Nodes = &ladr.Nodes{Kind: ladr.NodesNumber, Num: float64(*dynCfg.Nodes)}
			rawNodeCount = *dynCfg.Nodes
			nodeCount = minInt(rawNodeCount, budget.MaxNodesPerStep)
		}
		// Update shouldUseNumberedNodes since base.Nodes is now set.
	}

	// shouldUseNumberedNodes: sequential steps or steps with explicit nodes
	shouldUseNumberedNodes := step.Kind == ladr.StepTypeSequential || base.Nodes != nil

	// executeNodeSafely wraps executeNode with error recovery + event publishing.
	executeNodeSafely := func(nodeNumber *int) {
		output, err := executeNode(ctx, step, nodeNumber, runtimeBase, io, stepsMap, provider, fallbackProviders, budget, llmCallCount)
		if err != nil {
			resultsMu.Lock()
			failedNodes = append(failedNodes, failedNode{NodeNumber: nodeNumber, Message: err.Error()})
			resultsMu.Unlock()
			_ = io.PublishEvent(ctx, PublishEventArgs{
				InstanceID: runtimeBase.InstanceID,
				Type:       EventNodeFailed,
				Data: map[string]any{
					"loopIndex":  runtimeBase.LoopIndex,
					"stepId":     step.ID(),
					"groupId":    runtimeBase.CurrentGroupID,
					"nodeNumber": nodeNumber,
					"message":    err.Error(),
				},
			})
			return
		}
		resultsMu.Lock()
		nodeResults = append(nodeResults, nodeResult{NodeNumber: nodeNumber, Output: output})
		outputs = append(outputs, output)
		resultsMu.Unlock()
	}

	switch {
	case nodeCount <= 0:
		// no-op
	case nodeCount == 1:
		if shouldUseNumberedNodes {
			n := 1
			executeNodeSafely(&n)
		} else {
			executeNodeSafely(nil)
		}
	case step.Kind == ladr.StepTypeSequential:
		for n := 1; n <= nodeCount; n++ {
			nodeNum := n
			executeNodeSafely(&nodeNum)
		}
	default:
		// Parallel batches of maxParallelNodes
		for batchStart := 0; batchStart < nodeCount; batchStart += maxParallelNodes {
			batchEnd := batchStart + maxParallelNodes
			if batchEnd > nodeCount {
				batchEnd = nodeCount
			}
			var wg sync.WaitGroup
			for offset := batchStart; offset < batchEnd; offset++ {
				nodeNum := offset + 1
				wg.Add(1)
				go func() {
					defer wg.Done()
					executeNodeSafely(&nodeNum)
				}()
			}
			wg.Wait()
		}
	}

	// Build pruned map for multi-node steps
	if nodeCount > 1 {
		var surviving []int
		for _, r := range nodeResults {
			if r.NodeNumber != nil {
				surviving = append(surviving, *r.NodeNumber)
			}
		}
		if runtimeBase.PrunedNodeMap == nil {
			runtimeBase.PrunedNodeMap = NewPrunedNodeMap()
		}
		runtimeBase.PrunedNodeMap.Set(GetPrunedNodeMapKey(runtimeBase.LoopIndex, step.ID()), surviving)
	}

	// continueIf gate
	if base.ContinueIf != "" {
		if nodeCount <= 1 {
			only := ""
			if len(outputs) > 0 {
				only = outputs[0]
			}
			if only != base.ContinueIf {
				outputs = nil
				nodeResults = nil
				if shouldUseNumberedNodes {
					if runtimeBase.PrunedNodeMap == nil {
						runtimeBase.PrunedNodeMap = NewPrunedNodeMap()
					}
					runtimeBase.PrunedNodeMap.Set(GetPrunedNodeMapKey(runtimeBase.LoopIndex, step.ID()), []int{})
				}
				return "", fmt.Errorf(`Step "%s" failed continueIf gate.`, step.ID())
			}
		} else {
			var surviving []int
			for _, r := range nodeResults {
				if r.Output == base.ContinueIf && r.NodeNumber != nil {
					surviving = append(surviving, *r.NodeNumber)
				}
			}
			if runtimeBase.PrunedNodeMap == nil {
				runtimeBase.PrunedNodeMap = NewPrunedNodeMap()
			}
			runtimeBase.PrunedNodeMap.Set(GetPrunedNodeMapKey(runtimeBase.LoopIndex, step.ID()), surviving)

			var filtered []nodeResult
			for _, r := range nodeResults {
				if r.NodeNumber != nil && containsInt(surviving, *r.NodeNumber) {
					filtered = append(filtered, r)
				}
			}
			nodeResults = filtered
			outputs = nil
			for _, r := range filtered {
				outputs = append(outputs, r.Output)
			}
		}

		if len(nodeResults) == 0 && len(failedNodes) == 0 {
			return "", fmt.Errorf(
				`Step "%s" aborted: all %d nodes were pruned by continueIf gate ("%s").`,
				step.ID(), nodeCount, base.ContinueIf,
			)
		}
	}

	// Phase 1: New typed If-block gate evaluation.
	// If and continueIf are mutually exclusive (validated in validateNoContinueIfConflict).
	// When If is declared, continueIf is empty so the above block is a no-op.
	if base.If != nil {
		outcome := DispatchGate(base, nodeResults)

		// Update nodeResults to only include survivors from DispatchGate.
		// Numbered results are kept by survivor number; unnumbered results
		// (single-node steps without explicit nodes:) are kept exactly when
		// the gate passed — before GatePassed this filter wiped a passing
		// step's only output.
		survivorSet := make(map[int]bool)
		for _, n := range outcome.Survivors {
			survivorSet[n] = true
		}
		var filtered []nodeResult
		for _, r := range nodeResults {
			if r.NodeNumber != nil {
				if survivorSet[*r.NodeNumber] {
					filtered = append(filtered, r)
				}
			} else if outcome.GatePassed {
				filtered = append(filtered, r)
			}
		}
		nodeResults = filtered
		outputs = nil
		for _, r := range filtered {
			outputs = append(outputs, r.Output)
		}

		// Update pruned node map for downstream consumers.
		if shouldUseNumberedNodes || nodeCount > 1 {
			if runtimeBase.PrunedNodeMap == nil {
				runtimeBase.PrunedNodeMap = NewPrunedNodeMap()
			}
			runtimeBase.PrunedNodeMap.Set(GetPrunedNodeMapKey(runtimeBase.LoopIndex, step.ID()), outcome.Survivors)
		}

		// Handle step-level action (then/else outcome).
		switch outcome.Action {
		case OutcomeJump:
			if outcome.JumpStepID != "" {
				runtimeBase.PendingJump = outcome.JumpStepID
				runtimeBase.PendingPassFields = outcome.PassFields
			}
		case OutcomeHandoff:
			// Cross-ladder jump (tail-call). Capture target for the pipeline loop
			// to consume on the next iteration; the pipeline terminates current
			// execution and spawns the target ladder via io.Handoff.
			if outcome.HandoffTarget != nil {
				runtimeBase.PendingHandoff = outcome.HandoffTarget
				runtimeBase.PendingPassFields = outcome.PassFields
			}
		case OutcomeAbort:
			if len(nodeResults) == 0 && len(failedNodes) == 0 {
				return "", fmt.Errorf(
					`Step "%s" aborted: gate condition not met and else action is abort.`,
					step.ID(),
				)
			}
		}
	}

	// Publish node_survivors event
	if shouldUseNumberedNodes || len(failedNodes) > 0 {
		var survivingNumbers []int
		for _, r := range nodeResults {
			if r.NodeNumber != nil {
				survivingNumbers = append(survivingNumbers, *r.NodeNumber)
			}
		}
		_ = io.PublishEvent(ctx, PublishEventArgs{
			InstanceID: runtimeBase.InstanceID,
			Type:       EventNodeSurvivors,
			Data: map[string]any{
				"loopIndex":            runtimeBase.LoopIndex,
				"stepId":               step.ID(),
				"groupId":              runtimeBase.CurrentGroupID,
				"requestedNodes":       nodeCount,
				"survivingNodes":       len(nodeResults),
				"failedNodes":          len(failedNodes),
				"survivingNodeNumbers": survivingNumbers,
			},
		})
	}

	if nodeCount > 0 && len(nodeResults) == 0 && len(failedNodes) > 0 {
		return "", NewNodeDependencyError(fmt.Sprintf(`All nodes failed for step "%s".`, step.ID()))
	}

	// Write combined output
	combined := joinStrings(outputs, "\n\n")
	if err := io.WriteOutput(ctx, WriteOutputArgs{
		InstanceID: runtimeBase.InstanceID,
		LoopIndex:  runtimeBase.LoopIndex,
		GroupID:    runtimeBase.CurrentGroupID,
		StepID:     step.ID(),
		Output:     combined,
	}); err != nil {
		return "", err
	}

	// Phase 1: Apply schema-matched relikWrite (if declared).
	if base.RelikWrite != nil && runtimeBase.RelikStore != nil {
		we := NewWriteExecutor(runtimeBase.RelikStore)
		if err := we.ApplyRelikWrite(combined, *base.RelikWrite); err != nil {
			// Write failures are non-fatal — log but continue. No-ops are
			// equally non-fatal but carry their own event so a silent
			// "wrote nothing" is observable in the trace.
			var noop *WriteNoOpError
			msg := "relikWrite failed: " + err.Error()
			if errors.As(err, &noop) {
				msg = err.Error()
			}
			_ = io.PublishEvent(ctx, PublishEventArgs{
				InstanceID: runtimeBase.InstanceID,
				Type:       EventError,
				Data:       map[string]any{"message": msg, "nonFatal": true, "stepId": step.ID(), "noOp": noop != nil},
			})
		} else {
			_ = io.PublishEvent(ctx, PublishEventArgs{
				InstanceID: runtimeBase.InstanceID,
				Type:       EventToolResult,
				Data:       map[string]any{"message": "relikWrite applied", "stepId": step.ID()},
			})
		}
	}

	
	// Post-step mail send (if declared). The envelope addresses a ladder by
	// program name; the run's owner scopes it. Failures are non-fatal — a
	// dropped notice must not kill the run that produced it.
	if base.Mail != nil && runtimeBase.Mailbox != nil {
		owner := runtimeBase.OwnerID
		if owner == "" {
			owner = runtimeBase.InstanceID
		}
		env := mailbox.Envelope{
			Sender:        runtimeBase.LadderName,
			Type:          base.Mail.Type,
			CorrelationID: base.Mail.CorrelationID,
			RelikRef:      base.Mail.RelikRef,
		}
		if env.Type == "" {
			env.Type = "notice"
		}
		if err := runtimeBase.Mailbox.Post(ctx, owner, base.Mail.To, env); err != nil {
			_ = io.PublishEvent(ctx, PublishEventArgs{
				InstanceID: runtimeBase.InstanceID,
				Type:       EventError,
				Data:       map[string]any{"message": "mail post failed: " + err.Error(), "nonFatal": true},
			})
		}
	}

	return combined, nil
}

// MaybeRecurse checks whether the step should recurse, and if so calls
// io.Recurse to produce a child output.
func MaybeRecurse(
	ctx context.Context,
	step ladr.Step,
	output string,
	runtimeBase *ExecutionRuntimeContext,
	io EngineIO,
	config *ladr.PipelineConfig,
	provider ProviderConfig,
	fallbackProviders []ProviderConfig,
	budget ResourceBudget,
) (string, error) {
	base := step.Base()
	if base == nil || base.Recursion == nil {
		return output, nil
	}

	var maxDepth int
	md := base.Recursion.MaxDepthNumOrStr
	if !md.IsStr {
		maxDepth = int(md.Num)
	} else {
		knobName, _ := ParseKnobReference(md.Str)
		val := 1.0
		if knobName != "" {
			if v, ok := runtimeBase.Knobs[knobName]; ok {
				val = v
			}
		}
		maxDepth = int(val)
	}

	clampedDepth := minInt(maxDepth, budget.MaxRecursionDepth)
	if runtimeBase.Depth >= maxInt(0, clampedDepth) {
		return output, nil
	}

	childOutput, err := io.Recurse(ctx, RecurseArgs{
		ParentInstanceID:  runtimeBase.InstanceID,
		StepID:            step.ID(),
		Context:           output,
		Depth:             runtimeBase.Depth + 1,
		Knobs:             runtimeBase.Knobs,
		Config:            config,
		Provider:          provider,
		FallbackProviders: fallbackProviders,
	})
	if err != nil {
		return "", err
	}
	return childOutput, nil
}

// CollectExecutableSteps builds a map of step ID → step for all non-group
// (executable) steps, flattening group steps.
func CollectExecutableSteps(steps []ladr.Step) map[string]ladr.Step {
	out := make(map[string]ladr.Step)
	for _, step := range steps {
		if step.Kind == ladr.StepTypeGroup && step.Group != nil {
			for _, child := range step.Group.Steps {
				if IsStep(child) {
					out[child.ID()] = child
				}
			}
			continue
		}
		out[step.ID()] = step
	}
	return out
}

// --- helpers ---

func containsInt(slice []int, v int) bool {
	for _, item := range slice {
		if item == v {
			return true
		}
	}
	return false
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for i := 1; i < len(ss); i++ {
		result += sep + ss[i]
	}
	return result
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	return int(math.Max(float64(a), float64(b)))
}
