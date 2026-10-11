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

// Package engine — step outcome types and action dispatch.
//
// A StepOutcome is the result of executing a step: which nodes survived gates,
// what action to take next (advance/jump/abort/handoff), and any pending writes.
package engine

import (
	"github.com/ladr/ladr/internal/ladr"
)

// OutcomeAction is what the cursor should do after a step completes.
type OutcomeAction int

const (
	// OutcomeAdvance — proceed to the next step.
	OutcomeAdvance OutcomeAction = iota
	// OutcomeJump — reposition cursor to a specific step (backward or forward).
	OutcomeJump
	// OutcomeAbort — stop ladder execution entirely.
	OutcomeAbort
	// OutcomeHandoff — cross-ladder jump; terminate current, start target.
	OutcomeHandoff
)

// StepOutcome is the result of executing a step with gates evaluated.
type StepOutcome struct {
	Action        OutcomeAction
	JumpStepID    string           // for OutcomeJump (within-ladder)
	HandoffTarget *ladr.JumpTarget // for OutcomeHandoff (cross-ladder)
	PassFields    []ladr.PassField // for OutcomeJump AND OutcomeHandoff: caller-supplied field overrides
	Survivors     []int            // surviving node numbers (after per-node continue gate)
	BestOutput    string           // best-so-far output (for graceful exit on ceiling exhaustion)

	// GatePassed reports whether the gate's condition held for at least one
	// node output (the then-branch fired). Survivors carries node NUMBERS,
	// which unnumbered single-node steps never have — GatePassed is the
	// number-agnostic signal. Without it, a passing gate on an unnumbered
	// step was indistinguishable from a failing one.
	GatePassed bool
}

// DispatchGate evaluates the step-level if/then/else after per-node pruning.
//
// Logic (per spec §4):
//  1. Per-node continue: each node checked individually; non-matching pruned.
//  2. After pruning, if any survivors remain → fire then action.
//  3. If no survivors → fire else action (default: abort).
//  4. If the step has no If block but has continueIf, desugar to if: { equals: X }.
func DispatchGate(
	step *ladr.BaseStep,
	nodeOutputs []nodeResult,
) StepOutcome {
	// No gate declared — all nodes survive, advance.
	if step.If == nil && step.ContinueIf == "" {
		var survivors []int
		for _, r := range nodeOutputs {
			if r.NodeNumber != nil {
				survivors = append(survivors, *r.NodeNumber)
			}
		}
		return StepOutcome{Action: OutcomeAdvance, Survivors: survivors}
	}

	// Build the effective If block (desugar continueIf if needed).
	var ifBlock *ladr.If
	if step.If != nil {
		ifBlock = step.If
	} else {
		// Desugar continueIf: "X" → if: { equals: "X", then: continue, else: abort }
		desugared := ladr.If{
			Condition: ladr.Condition{Kind: ladr.CondEquals, Equals: step.ContinueIf},
			Then:      ladr.ContinueAction(),
			Else:      ladr.AbortAction(),
		}
		ifBlock = &desugared
	}

	// Per-node continue: evaluate condition against each node output.
	var survivors []int
	var survivorOutputs []nodeResult
	for _, r := range nodeOutputs {
		result := EvaluateGate(r.Output, ifBlock.Condition)
		if result.Passed {
			if r.NodeNumber != nil {
				survivors = append(survivors, *r.NodeNumber)
			}
			survivorOutputs = append(survivorOutputs, r)
		}
	}

	// Step-level action. The decision keys on survivorOutputs, not survivors:
	// a single unnumbered node (steps without explicit nodes:) never
	// contributes a node number, so a survivors-only check fired the else
	// action on gates that actually passed.
	if len(survivorOutputs) > 0 {
		// Survivors exist → fire then action.
		outcome := resolveAction(ifBlock.Then, survivors, survivorOutputs)
		outcome.GatePassed = true
		return outcome
	}

	// No survivors → fire else action.
	bestOutput := ""
	if len(nodeOutputs) > 0 {
		bestOutput = nodeOutputs[0].Output
	}
	outcome := resolveAction(ifBlock.Else, nil, nil)
	outcome.BestOutput = bestOutput
	return outcome
}

// resolveAction translates an ladr.Action to a StepOutcome.
func resolveAction(action ladr.Action, survivors []int, survivorOutputs []nodeResult) StepOutcome {
	outcome := StepOutcome{Survivors: survivors}

	switch action.Kind {
	case ladr.ActionContinue:
		outcome.Action = OutcomeAdvance

	case ladr.ActionAbort:
		outcome.Action = OutcomeAbort

	case ladr.ActionJump:
		if action.Jump != nil {
			outcome.PassFields = action.Jump.PassFields
			if action.Jump.IsCrossLadder() {
				outcome.Action = OutcomeHandoff
				outcome.HandoffTarget = action.Jump
			} else {
				outcome.Action = OutcomeJump
				outcome.JumpStepID = action.Jump.StepID
			}
		} else {
			outcome.Action = OutcomeAdvance
		}

	default:
		outcome.Action = OutcomeAdvance
	}

	return outcome
}
