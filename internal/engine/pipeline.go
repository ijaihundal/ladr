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

// Package engine — pipeline orchestration.
// Ported from packages/engine-core/pipeline.ts.
package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/ladr/ladr/internal/ladr"
	"golang.org/x/sync/errgroup"
)

// stepOutputForEvent carries a step's output in its step_complete event,
// truncated: the full text can be huge, but what each step actually said
// must be queryable after the run — forensics on an arbitration (did the
// reading audit run? what did the sweep execute?) need the trail, and
// nothing else persists it.
func stepOutputForEvent(out string) (string, bool) {
	const max = 4000
	if len(out) <= max {
		return out, false
	}
	return out[:max], true
}

// ExecutePipeline is the top-level pipeline orchestrator.
// It loops over maxLoops, iterating steps (sequential or group/parallel),
// handles resume from pause, abort via context, resolves exit step output,
// and publishes lifecycle events.
func ExecutePipeline(ctx context.Context, opts ExecutePipelineOptions) (string, error) {
	config := opts.Config
	runtimeCtx := opts.Context
	runtimeCtx.Config = config
	io := opts.IO
	provider := opts.Provider
	fallbackProviders := opts.FallbackProviders
	maxLoops := opts.MaxLoops
	debugMode := opts.DebugMode
	resumeFrom := opts.ResumeFrom

	budget := DefaultResourceBudget
	if opts.Budget != nil {
		budget = *opts.Budget
	}

	// Phase 1: Initialize resource ceilings (Path B safety net).
	ceilings := ResolveCeilings(config.ExecutionBudget)
	var ceilingTracker *CeilingTracker
	if opts.CeilingTracker != nil {
		ceilingTracker = opts.CeilingTracker
	} else {
		ceilingTracker = NewCeilingTracker(ceilings)
	}
	runtimeCtx.CeilingTracker = ceilingTracker

	// Phase 1: Initialize relik store (persistent memory).
	if opts.RelikStore != nil && len(config.Reliks) > 0 {
		// Only declared reliks seed; references never auto-init.
		var declared []ladr.RelikDecl
		for _, decl := range config.Reliks {
			if !decl.IsReference() {
				declared = append(declared, decl)
			}
		}
		if len(declared) > 0 {
			if err := opts.RelikStore.Load(declared); err != nil {
				return "", fmt.Errorf("relik load failed: %w", err)
			}
		}
		// Reference-mode reliks bind an existing relik by title — require it
		// now, before any LLM call, so a missing dependency fails fast
		// instead of mid-run.
		for _, decl := range config.Reliks {
			if decl.IsReference() && !opts.RelikStore.HasRelik(decl.Title) {
				return "", fmt.Errorf("relik %q is referenced but does not exist in this relik instance — declare it in its owning ladder or create it first", decl.Title)
			}
		}
		runtimeCtx.RelikStore = opts.RelikStore
	}

	stepsMap := CollectExecutableSteps(config.Steps)
	prunedNodeMap := NewPrunedNodeMap()
	var llmCallCount int64

	clampedLoops := minInt(maxLoops, budget.MaxLoops)
	if clampedLoops < maxLoops {
		_ = io.PublishEvent(ctx, PublishEventArgs{
			InstanceID: runtimeCtx.InstanceID,
			Type:       EventNodeCountResolved,
			Data: map[string]any{
				"maxLoops": clampedLoops,
				"clamped":  true,
				"limit":    budget.MaxLoops,
			},
		})
	}

	startLoop := 0
	isResuming := resumeFrom != nil
	if isResuming {
		startLoop = resumeFrom.LoopIndex
		_ = io.PublishEvent(ctx, PublishEventArgs{
			InstanceID: runtimeCtx.InstanceID,
			Type:       EventPipelineResumed,
			Data: map[string]any{
				"loopIndex": resumeFrom.LoopIndex,
				"stepId":    resumeFrom.StepID,
			},
		})
	}

	// Phase 3: If this instance was spawned by a cross-ladder handoff with
	// passFields, the parent shipped the resolved overrides under the reserved
	// __passfields input key. Extract them onto the runtime context so the
	// target step sees them as PassFieldOverrides.
	//
	// Validation of override targets happens after deferFunc is defined (below).
	if pfRaw, ok := runtimeCtx.Inputs["__passfields"]; ok {
		if pfMap, ok := pfRaw.(map[string]string); ok && len(pfMap) > 0 {
			runtimeCtx.PassFieldOverrides = pfMap
		} else if pfAny, ok := pfRaw.(map[string]any); ok && len(pfAny) > 0 {
			// Deserialize from any-typed map (typical when crossing the JSON boundary).
			runtimeCtx.PassFieldOverrides = make(map[string]string, len(pfAny))
			for k, v := range pfAny {
				if s, ok := v.(string); ok {
					runtimeCtx.PassFieldOverrides[k] = s
				}
			}
		}
	}

	// checkPause returns PipelinePausedError if the engine should pause.
	checkPause := func(loopIndex, stepIndex int, stepID string) error {
		pauseRequested := opts.PauseRequestCheck != nil && opts.PauseRequestCheck()
		if !debugMode && !pauseRequested {
			return nil
		}
		should := pauseRequested
		if !should {
			var err error
			should, err = io.ShouldPause(ctx, stepID)
			if err != nil {
				return err
			}
		}
		if should {
			pos := PausedPosition{
				LoopIndex: loopIndex,
				StepIndex: stepIndex,
				StepID:    stepID,
			}
			if err := io.SetPauseState(ctx, pos); err != nil {
				return err
			}
			_ = io.PublishEvent(ctx, PublishEventArgs{
				InstanceID: runtimeCtx.InstanceID,
				Type:       EventStepPaused,
				Data: map[string]any{
					"loopIndex": loopIndex,
					"stepIndex": stepIndex,
					"stepId":    stepID,
				},
			})
			return &PipelinePausedError{Position: pos}
		}
		return nil
	}

	// Handle errors: re-raise PipelinePausedError, otherwise publish error event.
	deferFunc := func(err error) error {
		if err == nil {
			return nil
		}
		var ppe *PipelinePausedError
		if errors.As(err, &ppe) {
			return err
		}
		_ = io.PublishEvent(ctx, PublishEventArgs{
			InstanceID: runtimeCtx.InstanceID,
			Type:       EventError,
			Data:       map[string]any{"message": err.Error()},
		})
		return err
	}

	// --- pipeline_start ---
	if !isResuming {
		if err := io.PublishEvent(ctx, PublishEventArgs{
			InstanceID: runtimeCtx.InstanceID,
			Type:       EventPipelineStart,
			Data: map[string]any{
				"maxLoops": clampedLoops,
				"ceilings": ceilingTracker.Snapshot()["ceilings"],
			},
		}); err != nil {
			return "", deferFunc(err)
		}
	}

	// Build step index map for jump support (stepId → position in config.Steps).
	stepIndexByID := make(map[string]int)
	for i, s := range config.Steps {
		stepIndexByID[s.ID()] = i
	}

	// Phase 3.4: Runtime validation of cross-ladder passField targets.
	// Determine which step is the handoff destination, then ensure every override
	// key matches a declared field on that step. Run AFTER deferFunc is defined
	// so the error path publishes a proper engine event.
	if len(runtimeCtx.PassFieldOverrides) > 0 {
		targetStepID, _ := runtimeCtx.Inputs["input.handoffTargetStep"].(string)
		if v, ok := runtimeCtx.Inputs["input.handoffTarget"].(map[string]any); ok {
			if s, ok := v["stepId"].(string); ok && s != "" {
				targetStepID = s
			}
		}
		if verr := validateHandoffPassFieldTargets(config, targetStepID, runtimeCtx.PassFieldOverrides); verr != nil {
			return "", deferFunc(verr)
		}
	}

	// --- main loop ---
	for loopIndex := startLoop; loopIndex < clampedLoops; loopIndex++ {
		// Check cancellation
		if err := ctx.Err(); err != nil {
			return "", deferFunc(fmt.Errorf("Pipeline aborted: %s", err.Error()))
		}

		if loopIndex != startLoop || !isResuming {
			if err := io.PublishEvent(ctx, PublishEventArgs{
				InstanceID: runtimeCtx.InstanceID,
				Type:       EventLoopStart,
				Data:       map[string]any{"loopIndex": loopIndex},
			}); err != nil {
				return "", deferFunc(err)
			}
		}

		// Phase 2 follow-up: mid-ladder entry on cross-ladder handoff. If this
		// instance was spawned by a handoff that targeted a specific stepId,
		// skip ahead to that step on the FIRST loop iteration. Subsequent loops
		// (if any) run from step 0 normally — the entry point only applies to
		// the initial handoff descent.
		stepIdx := 0
		if loopIndex == startLoop {
			if entryStepID, ok := runtimeCtx.Inputs["input.handoffTargetStep"].(string); ok && entryStepID != "" {
				if entryIdx, found := stepIndexByID[entryStepID]; found && entryIdx > 0 {
					stepIdx = entryIdx
					_ = io.PublishEvent(ctx, PublishEventArgs{
						InstanceID: runtimeCtx.InstanceID,
						Type:       EventStepStart,
						Data: map[string]any{
							"midLadderEntry": true,
							"entryStepId":    entryStepID,
							"skippedSteps":   entryIdx,
							"loopIndex":      loopIndex,
						},
					})
				}
			}
		}
		for stepIdx < len(config.Steps) {
			item := config.Steps[stepIdx]
			// Skip already-completed steps when resuming
			if isResuming && loopIndex == startLoop && stepIdx <= resumeFrom.StepIndex {
				stepIdx++
				continue
			}

			// Phase 1: Resource ceiling check — each step costs 1 hop.
			if err := ceilingTracker.IncHop(); err != nil {
				_ = io.PublishEvent(ctx, PublishEventArgs{
					InstanceID: runtimeCtx.InstanceID,
					Type:       EventError,
					Data:       map[string]any{"message": err.Error(), "ceiling_exceeded": true},
				})
				return "", deferFunc(err)
			}

			if item.Kind == ladr.StepTypeGroup && item.Group != nil {
				// Group: execute child steps in parallel
				g, ctx2 := errgroup.WithContext(ctx)
				for _, child := range item.Group.Steps {
					child := child
					if !IsStep(child) {
						continue
					}
					g.Go(func() error {
						runtimeBase := runtimeCtx
						runtimeBase.PrunedNodeMap = prunedNodeMap
						runtimeBase.LoopIndex = loopIndex
						runtimeBase.CurrentStepID = child.ID()
						runtimeBase.CurrentGroupID = item.ID()

						if err := io.PublishEvent(ctx2, PublishEventArgs{
							InstanceID: runtimeCtx.InstanceID,
							Type:       EventStepStart,
							Data: map[string]any{
								"loopIndex": loopIndex,
								"stepId":    child.ID(),
								"groupId":   item.ID(),
							},
						}); err != nil {
							return err
						}

						output, err := ExecuteSingleStep(ctx2, child, &runtimeBase, io, stepsMap, provider, fallbackProviders, budget, &llmCallCount)
						if err != nil {
							return err
						}

						recursiveOutput, err := MaybeRecurse(ctx2, child, output, &runtimeBase, io, config, provider, fallbackProviders, budget)
						if err != nil {
							return err
						}
						if recursiveOutput != output {
							if err := io.WriteOutput(ctx2, WriteOutputArgs{
								InstanceID: runtimeCtx.InstanceID,
								LoopIndex:  loopIndex,
								GroupID:    item.ID(),
								StepID:     child.ID(),
								Output:     recursiveOutput,
							}); err != nil {
								return err
							}
						}

						outForEvent, truncated := stepOutputForEvent(output)
						return io.PublishEvent(ctx2, PublishEventArgs{
							InstanceID: runtimeCtx.InstanceID,
							Type:       EventStepComplete,
							Data: map[string]any{
								"loopIndex":       loopIndex,
								"stepId":          child.ID(),
								"groupId":         item.ID(),
								"output":          outForEvent,
								"outputTruncated": truncated,
							},
						})
					})
				}
				if err := g.Wait(); err != nil {
					return "", deferFunc(err)
				}
				if err := checkPause(loopIndex, stepIdx, item.ID()); err != nil {
					return "", deferFunc(err)
				}
				stepIdx++
				continue
			}

			// Normal/Sequential step
			runtimeBase := runtimeCtx
			runtimeBase.PrunedNodeMap = prunedNodeMap
			runtimeBase.LoopIndex = loopIndex
			runtimeBase.CurrentStepID = item.ID()
			runtimeBase.CurrentGroupID = ""

			if err := io.PublishEvent(ctx, PublishEventArgs{
				InstanceID: runtimeCtx.InstanceID,
				Type:       EventStepStart,
				Data: map[string]any{
					"loopIndex": loopIndex,
					"stepId":    item.ID(),
				},
			}); err != nil {
				return "", deferFunc(err)
			}

			// System prompt override
			effectiveStep := item
			override, err := io.GetSystemPromptOverride(ctx, item.ID())
			if err == nil && override != "" {
				if base := effectiveStep.Base(); base != nil {
					// Deep-copy the step with the overridden system prompt
					newBase := *base
					newBase.SystemPrompt = override
					effectiveStep = stepWithBase(item, newBase)
				}
			}

			output, err := ExecuteSingleStep(ctx, effectiveStep, &runtimeBase, io, stepsMap, provider, fallbackProviders, budget, &llmCallCount)
			// Phase 3: Clear passField overrides after the target step completes.
			// Overrides apply to ONE step only — the target of the jump that
			// produced them. Subsequent steps see an empty override map.
			// Clear on runtimeCtx so the next iteration's copy sees nil.
			runtimeCtx.PassFieldOverrides = nil
			skipPostStepProcessing := false
			var recursiveOutput string
			if err != nil {
				// Dynamic step load failure with onInvalid: dispatch the action
				// (jump/abort) instead of propagating a fatal error.
				var dle *dynamicLoadError
				if errors.As(err, &dle) {
					skipPostStepProcessing = true
					switch dle.action.Kind {
					case ladr.ActionJump:
						if dle.action.Jump != nil {
							if dle.action.Jump.IsCrossLadder() {
								runtimeBase.PendingHandoff = dle.action.Jump
							} else {
								runtimeBase.PendingJump = dle.action.Jump.StepID
								runtimeBase.PendingPassFields = dle.action.Jump.PassFields
							}
						}
					case ladr.ActionAbort:
						return "", deferFunc(fmt.Errorf("step %q: dynamic load failed and onInvalid is abort: %w", item.ID(), err))
					}
				} else {
					return "", deferFunc(err)
				}
			}

			if !skipPostStepProcessing {
				recursiveOutput, err = MaybeRecurse(ctx, item, output, &runtimeBase, io, config, provider, fallbackProviders, budget)
				if err != nil {
					return "", deferFunc(err)
				}
				if recursiveOutput != output {
					if err := io.WriteOutput(ctx, WriteOutputArgs{
						InstanceID: runtimeCtx.InstanceID,
						LoopIndex:  loopIndex,
						StepID:     item.ID(),
						Output:     recursiveOutput,
					}); err != nil {
						return "", deferFunc(err)
					}
				}

				// Phase 1: Track last step output for dynamic step resolution (from: previous).
				runtimeCtx.LastStepID = item.ID()
				runtimeCtx.LastStepOutput = recursiveOutput

				outForEvent, truncated := stepOutputForEvent(recursiveOutput)
				if err := io.PublishEvent(ctx, PublishEventArgs{
					InstanceID: runtimeCtx.InstanceID,
					Type:       EventStepComplete,
					Data: map[string]any{
						"loopIndex":       loopIndex,
						"stepId":          item.ID(),
						"output":          outForEvent,
						"outputTruncated": truncated,
					},
				}); err != nil {
					return "", deferFunc(err)
				}

				if err := checkPause(loopIndex, stepIdx, item.ID()); err != nil {
					return "", deferFunc(err)
				}
			}

			// Phase 1: Check for pending jump from gate evaluation.
			if runtimeBase.PendingJump != "" {
				if targetIdx, ok := stepIndexByID[runtimeBase.PendingJump]; ok {
					_ = io.PublishEvent(ctx, PublishEventArgs{
						InstanceID: runtimeCtx.InstanceID,
						Type:       EventStepStart,
						Data:       map[string]any{"jump": runtimeBase.PendingJump, "from": item.ID(), "loopIndex": loopIndex},
					})
					// Phase 3: Resolve passFields in CALLER's runtime and stage them
					// as overrides for the target step's field resolution.
					if len(runtimeBase.PendingPassFields) > 0 {
						overrides, oerr := resolvePassFieldOverrides(ctx, runtimeBase.PendingPassFields, &runtimeBase, io, recursiveOutput)
						if oerr != nil {
							return "", deferFunc(fmt.Errorf("jump from step %q passField resolution failed: %w", item.ID(), oerr))
						}
						// Set on runtimeCtx so the next loop iteration's copy picks them up.
						runtimeCtx.PassFieldOverrides = overrides
					}
					runtimeBase.PendingJump = ""
					runtimeBase.PendingPassFields = nil
					stepIdx = targetIdx
					continue
				}
			}

			// Phase 2: Check for pending cross-ladder handoff. Tail-call semantics:
			// terminate the current execution and return the child's exit output
			// as the user-visible result.
			if runtimeBase.PendingHandoff != nil {
				target := *runtimeBase.PendingHandoff
				passFields := runtimeBase.PendingPassFields
				runtimeBase.PendingHandoff = nil
				runtimeBase.PendingPassFields = nil

				// Phase 3: Resolve passFields in CALLER's runtime and ship the
				// resulting {fieldName: value} map to the child via HandoffArgs.
				var passFieldOverrides map[string]string
				if len(passFields) > 0 {
					overrides, oerr := resolvePassFieldOverrides(ctx, passFields, &runtimeBase, io, recursiveOutput)
					if oerr != nil {
						return "", deferFunc(fmt.Errorf("handoff from step %q passField resolution failed: %w", item.ID(), oerr))
					}
					passFieldOverrides = overrides
				}

				handoffResult, err := io.Handoff(ctx, HandoffArgs{
					ParentInstanceID:   runtimeCtx.InstanceID,
					FromStepID:         item.ID(),
					FiringOutput:       recursiveOutput,
					Target:             target,
					Knobs:              runtimeCtx.Knobs,
					Provider:           provider,
					FallbackProviders:  fallbackProviders,
					PassFieldOverrides: passFieldOverrides,
				})
				if err != nil {
					return "", deferFunc(fmt.Errorf("handoff from step %q to %s failed: %w", item.ID(), target.LadderID, err))
				}
				// Publish pipeline_complete with the child's exit output as the result.
				_ = io.PublishEvent(ctx, PublishEventArgs{
					InstanceID: runtimeCtx.InstanceID,
					Type:       EventPipelineComplete,
					Data: map[string]any{
						"output":          handoffResult.ExitOutput,
						"handoff":         true,
						"childInstanceId": handoffResult.ChildInstanceID,
						"targetLadderId":  target.LadderID,
					},
				})
				return handoffResult.ExitOutput, nil
			}
			stepIdx++
		}

		if err := io.PublishEvent(ctx, PublishEventArgs{
			InstanceID: runtimeCtx.InstanceID,
			Type:       EventLoopComplete,
			Data:       map[string]any{"loopIndex": loopIndex},
		}); err != nil {
			return "", deferFunc(err)
		}
	}

	// --- resolve exit step output ---
	finalOutput, err := resolveExitOutput(ctx, config, stepsMap, prunedNodeMap, &runtimeCtx, io, clampedLoops)
	if err != nil {
		return "", deferFunc(err)
	}

	if err := io.PublishEvent(ctx, PublishEventArgs{
		InstanceID: runtimeCtx.InstanceID,
		Type:       EventPipelineComplete,
		Data:       map[string]any{"output": finalOutput},
	}); err != nil {
		return "", deferFunc(err)
	}

	return finalOutput, nil
}

// resolveExitOutput resolves the final output from the exit step.
func resolveExitOutput(
	ctx context.Context,
	config *ladr.PipelineConfig,
	stepsMap map[string]ladr.Step,
	prunedNodeMap *PrunedNodeMap,
	runtimeCtx *ExecutionRuntimeContext,
	io EngineIO,
	clampedLoops int,
) (string, error) {
	exitStep, ok := stepsMap[config.Exit]
	if !ok {
		// Fallback: just read the exit step output directly
		output, err := io.ReadOutput(ctx, ReadOutputArgs{
			InstanceID: runtimeCtx.InstanceID,
			LoopIndex:  clampedLoops - 1,
			StepID:     config.Exit,
		})
		if err != nil {
			return "", err
		}
		return output, nil
	}

	base := exitStep.Base()
	if exitStep.Kind == ladr.StepTypeSequential && base != nil && base.Nodes != nil {
		prunedKey := GetPrunedNodeMapKey(clampedLoops-1, config.Exit)
		prunedNodes, _ := prunedNodeMap.Get(prunedKey)
		if len(prunedNodes) > 0 {
			lastSurvivingNode := prunedNodes[len(prunedNodes)-1]
			output, err := io.ReadOutput(ctx, ReadOutputArgs{
				InstanceID: runtimeCtx.InstanceID,
				LoopIndex:  clampedLoops - 1,
				StepID:     config.Exit,
				NodeNumber: &lastSurvivingNode,
			})
			if err != nil {
				return "", err
			}
			if err := io.WriteOutput(ctx, WriteOutputArgs{
				InstanceID: runtimeCtx.InstanceID,
				LoopIndex:  clampedLoops - 1,
				StepID:     config.Exit,
				Output:     output,
			}); err != nil {
				return "", err
			}
			return output, nil
		}

		// No pruned survivors — resolve node count and read the last node
		exitRuntime := *runtimeCtx
		exitRuntime.PrunedNodeMap = prunedNodeMap
		exitRuntime.LoopIndex = clampedLoops - 1
		exitRuntime.CurrentStepID = config.Exit
		exitNodeCount, err := ResolveNodeCount(ctx, base.Nodes, &exitRuntime, io)
		if err != nil {
			return "", err
		}
		if exitNodeCount > 0 {
			exitOutput, err := io.ReadOutput(ctx, ReadOutputArgs{
				InstanceID: runtimeCtx.InstanceID,
				LoopIndex:  clampedLoops - 1,
				StepID:     config.Exit,
				NodeNumber: &exitNodeCount,
			})
			if err != nil {
				return "", err
			}
			if exitOutput == "" {
				return "", fmt.Errorf(`Exit step "%s" node %d produced no output`, config.Exit, exitNodeCount)
			}
			if err := io.WriteOutput(ctx, WriteOutputArgs{
				InstanceID: runtimeCtx.InstanceID,
				LoopIndex:  clampedLoops - 1,
				StepID:     config.Exit,
				Output:     exitOutput,
			}); err != nil {
				return "", err
			}
			return exitOutput, nil
		}
		// exitNodeCount == 0: fall through to write empty output
		if err := io.WriteOutput(ctx, WriteOutputArgs{
			InstanceID: runtimeCtx.InstanceID,
			LoopIndex:  clampedLoops - 1,
			StepID:     config.Exit,
			Output:     "",
		}); err != nil {
			return "", err
		}
		return "", nil
	}

	// Non-sequential or no nodes: read directly
	output, err := io.ReadOutput(ctx, ReadOutputArgs{
		InstanceID: runtimeCtx.InstanceID,
		LoopIndex:  clampedLoops - 1,
		StepID:     config.Exit,
	})
	if err != nil {
		return "", err
	}
	return output, nil
}

// stepWithBase returns a copy of step with its BaseStep replaced.
func stepWithBase(step ladr.Step, newBase ladr.BaseStep) ladr.Step {
	switch step.Kind {
	case ladr.StepTypeNormal:
		ns := &ladr.NormalStep{BaseStep: newBase}
		return ladr.Step{Kind: step.Kind, Normal: ns}
	case ladr.StepTypeSequential:
		ss := &ladr.SequentialStep{BaseStep: newBase}
		return ladr.Step{Kind: step.Kind, Sequential: ss}
	default:
		return step
	}
}

// validateHandoffPassFieldTargets checks that every passField override key
// matches a declared field on the handoff destination step. Called at the
// start of the child pipeline when overrides were shipped from the caller.
//
// targetStepID selects which step to validate against:
//   - if "" or unmatched in the config: validate against the FIRST step
//     (the default entry point when no mid-ladder stepId was declared).
//   - if set and matches a step in the config: validate against that step.
//
// Returns an error wrapping the handoff_field_mismatch diagnostic. The firing
// step's declared onInvalid: action decides whether the error becomes a hard
// failure or a fallback branch (Phase 3 follow-up — currently errors propagate).
func validateHandoffPassFieldTargets(config *ladr.PipelineConfig, targetStepID string, overrides map[string]string) error {
	if len(overrides) == 0 {
		return nil
	}

	// Locate the destination step.
	var destStep ladr.Step
	if targetStepID != "" {
		for _, s := range config.Steps {
			if s.ID() == targetStepID {
				destStep = s
				break
			}
		}
		if destStep.Kind == "" {
			return fmt.Errorf("handoff_field_mismatch: target step %q not found in child ladder", targetStepID)
		}
	} else if len(config.Steps) > 0 {
		destStep = config.Steps[0]
	}
	if destStep.Kind == "" {
		return fmt.Errorf("handoff_field_mismatch: child ladder has no steps to validate against")
	}

	base := destStep.Base()
	if base == nil || base.Fields == nil || base.Fields.IsString {
		// Destination has no declared field list — every override key is unmatched.
		for k := range overrides {
			return fmt.Errorf("handoff_field_mismatch: override key %q targets step %q which has no declared field list", k, destStep.ID())
		}
		return nil
	}

	declared := make(map[string]bool, len(base.Fields.Fields))
	for _, f := range base.Fields.Fields {
		declared[fieldDisplayName(f)] = true
	}
	for k := range overrides {
		if !declared[k] {
			return fmt.Errorf("handoff_field_mismatch: override key %q does not match any declared field on target step %q", k, destStep.ID())
		}
	}
	return nil
}
