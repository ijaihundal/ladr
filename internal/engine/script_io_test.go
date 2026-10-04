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
)

// scriptIO is a scriptable EngineIO: ExecuteLlmCall returns a scripted sequence,
// ExecuteCode/FilesOp record calls and return a fixed result. Other methods are
// no-ops.
//
// Shared across test files that need a lightweight EngineIO double for
// pipeline-level tests (ceiling enforcement, etc.).
type scriptIO struct {
	llmOutputs []string // returned in order, one per ExecuteLlmCall
	llmIdx     int
	prompts    []string // captured UserPrompt values

	codeCalls  []ExecuteCodeParams
	codeResult ExecuteCodeResult
	codeErr    error

	filesCalls  []FilesOpParams
	filesResult FilesOpResult
	filesErr    error
}

func (s *scriptIO) ExecuteLlmCall(_ context.Context, a ExecuteLlmParams) (string, error) {
	s.prompts = append(s.prompts, a.UserPrompt)
	if s.llmIdx >= len(s.llmOutputs) {
		return "(no more scripted LLM output)", nil
	}
	out := s.llmOutputs[s.llmIdx]
	s.llmIdx++
	return out, nil
}
func (s *scriptIO) ExecuteCode(_ context.Context, a ExecuteCodeParams) (ExecuteCodeResult, error) {
	s.codeCalls = append(s.codeCalls, a)
	if s.codeErr != nil {
		return ExecuteCodeResult{}, s.codeErr
	}
	return s.codeResult, nil
}
func (s *scriptIO) FilesOp(_ context.Context, a FilesOpParams) (FilesOpResult, error) {
	s.filesCalls = append(s.filesCalls, a)
	if s.filesErr != nil {
		return FilesOpResult{}, s.filesErr
	}
	return s.filesResult, nil
}
func (s *scriptIO) ReadOutput(context.Context, ReadOutputArgs) (string, error) { return "", nil }
func (s *scriptIO) ReadOutputArray(context.Context, ReadOutputArrayArgs) ([]string, error) {
	return nil, nil
}
func (s *scriptIO) WriteOutput(context.Context, WriteOutputArgs) error   { return nil }
func (s *scriptIO) PublishEvent(context.Context, PublishEventArgs) error { return nil }
func (s *scriptIO) Recurse(context.Context, RecurseArgs) (string, error) { return "", nil }
func (s *scriptIO) Handoff(context.Context, HandoffArgs) (HandoffResult, error) {
	return HandoffResult{}, nil
}
func (s *scriptIO) SetPauseState(context.Context, PausedPosition) error             { return nil }
func (s *scriptIO) ShouldPause(context.Context, string) (bool, error)               { return false, nil }
func (s *scriptIO) GetSystemPromptOverride(context.Context, string) (string, error) { return "", nil }
func (s *scriptIO) GetNodeCount(context.Context, GetNodeCountArgs) (int, error)     { return 0, nil }
