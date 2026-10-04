// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Package trace holds the pipeline trace event types. The standalone
// export keeps the pure event surface; snapshot assembly and streaming
// against a live instance store ship with the server stack.
package trace

// TraceNodeOutput is one node's rendered output within a reasoning block.
type TraceNodeOutput struct {
	NodeNumber *int   `json:"nodeNumber"`
	Heading    string `json:"heading"`
	Text       string `json:"text"`
}

// TraceReasoningBlock groups node outputs for a single step/loop.
type TraceReasoningBlock struct {
	StepID    string            `json:"stepId"`
	Heading   string            `json:"heading"`
	LoopIndex int               `json:"loopIndex"`
	GroupID   *string           `json:"groupId"`
	Nodes     []TraceNodeOutput `json:"nodes"`
}

// TraceSnapshot is the full trace state for a completed instance.
type TraceSnapshot struct {
	InstanceID string                `json:"instanceId"`
	Status     string                `json:"status"`
	Mode       string                `json:"mode"`
	Blocks     []TraceReasoningBlock `json:"blocks"`
	Answer     *string               `json:"answer"`
}

// TraceStreamEventType discriminates TraceStreamEvent variants.
type TraceStreamEventType string

const (
	EventBlockStart     TraceStreamEventType = "block_start"
	EventReasoningToken TraceStreamEventType = "reasoning_token"
	EventAnswerToken    TraceStreamEventType = "answer_token"
	EventAnswerComplete TraceStreamEventType = "answer_complete"
	EventDone           TraceStreamEventType = "done"
)

// TraceStreamEvent is a union of all possible trace stream events.
// Only fields relevant to Type are populated.
type TraceStreamEvent struct {
	Type        TraceStreamEventType
	StepID      string // block_start, reasoning_token
	Heading     string // block_start
	LoopIndex   int    // block_start
	NodeNumber  *int   // block_start, reasoning_token
	NodeHeading string // block_start
	Text        string // reasoning_token, answer_token, answer_complete
	Status      string // done
}
