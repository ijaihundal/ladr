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

// Package engine — error types.
// Ported from packages/engine-core/errors.ts.
package engine

import (
	"fmt"

	"github.com/ladr/ladr/internal/ladr"
)

// PausedPosition identifies where a pipeline was paused.
type PausedPosition struct {
	LoopIndex          int
	StepIndex          int
	StepID             string
	EditedSystemPrompt string
}

// PipelinePausedError is returned when a pipeline is paused for debugging.
type PipelinePausedError struct {
	Position PausedPosition
}

func (e *PipelinePausedError) Error() string {
	return fmt.Sprintf(
		`Pipeline paused at step "%s" (loop %d)`,
		e.Position.StepID,
		e.Position.LoopIndex,
	)
}

// NodeDependencyError is returned when a required node output is missing.
type NodeDependencyError struct {
	Message string
}

func (e *NodeDependencyError) Error() string {
	return e.Message
}

// NewNodeDependencyError creates a NodeDependencyError.
func NewNodeDependencyError(message string) *NodeDependencyError {
	return &NodeDependencyError{Message: message}
}

// dynamicLoadError is returned by executeNode when a dynamic step's config fails
// to load and an onInvalid action is declared. The pipeline loop intercepts this
// error and dispatches the action (jump/write) instead of propagating a fatal.
type dynamicLoadError struct {
	action ladr.Action
	stepID string
}

func (e *dynamicLoadError) Error() string {
	return fmt.Sprintf("step %q: dynamic config load failed, firing onInvalid", e.stepID)
}
