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

// Package openai implements OpenAI-compatible response shapes for the gateway.
// Ported from packages/openai-shapes/shapes.ts.
package openai

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/trace"
)

// Usage represents token usage counts.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// StreamShaperOptions configures a StreamShaper.
type StreamShaperOptions struct {
	SessionID string
	Model     string
	Mode      ladr.TraceMode
	Created   int64 // optional; defaults to now
}

// ChatCompletionOptions configures the non-streaming chat completion.
type ChatCompletionOptions struct {
	SessionID string
	Model     string
	Mode      ladr.TraceMode
	Created   int64
	Usage     *Usage
}

// LadderModelEntry represents one model in the /v1/models response.
type LadderModelEntry struct {
	ID          string
	Author      string
	Name        string
	Description *string
}

// OpenAIErrorPayload is the error response body.
type OpenAIErrorPayload struct {
	Error OpenAIErrorDetail `json:"error"`
}

// OpenAIErrorDetail is the inner error object.
type OpenAIErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// OpenAIError creates an OpenAI-compatible error payload.
func OpenAIError(message, errType, code string) OpenAIErrorPayload {
	if errType == "" {
		errType = "invalid_request_error"
	}
	if code == "" {
		code = "internal_error"
	}
	return OpenAIErrorPayload{
		Error: OpenAIErrorDetail{
			Message: message,
			Type:    errType,
			Code:    code,
		},
	}
}

// --- helpers ---

func ts(created int64) int64 {
	if created != 0 {
		return created
	}
	return time.Now().Unix()
}

func sseJSON(v any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b) + "\n\n"
}

// --- StreamShaper ---

// StreamShaper converts TraceStreamEvents into OpenAI SSE chunks.
type StreamShaper interface {
	Push(event trace.TraceStreamEvent) string
	Finalize(status string, usage *Usage) string
}

// streamShaper is the concrete implementation of StreamShaper.
type streamShaper struct {
	sessionID       string
	model           string
	mode            ladr.TraceMode
	created         int64
	inlineThinkOpen bool
	finished        bool
}

// NewStreamShaper creates a new StreamShaper.
func NewStreamShaper(opts StreamShaperOptions) StreamShaper {
	return &streamShaper{
		sessionID: opts.SessionID,
		model:     opts.Model,
		mode:      opts.Mode,
		created:   ts(opts.Created),
	}
}

func (s *streamShaper) chunk(delta map[string]any, finishReason string) string {
	choice := map[string]any{
		"index":         0,
		"delta":         delta,
		"finish_reason": nil,
	}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	}
	return sseJSON(map[string]any{
		"id":      s.sessionID,
		"object":  "chat.completion.chunk",
		"created": s.created,
		"model":   s.model,
		"choices": []map[string]any{choice},
	})
}

func (s *streamShaper) Push(event trace.TraceStreamEvent) string {
	if s.finished {
		return ""
	}

	switch event.Type {
	case trace.EventBlockStart:
		if s.mode == ladr.TraceModeOff {
			return ""
		}
		heading := event.Heading
		if event.NodeNumber != nil && event.NodeHeading != "" {
			heading = event.NodeHeading
		}
		headingText := "### " + heading + "\n\n"
		if s.mode == ladr.TraceModeInline {
			if !s.inlineThinkOpen {
				s.inlineThinkOpen = true
				return s.chunk(map[string]any{"content": "<think>\n" + headingText}, "")
			}
			return s.chunk(map[string]any{"content": "\n" + headingText}, "")
		}
		return s.chunk(map[string]any{"reasoning_content": headingText}, "")

	case trace.EventReasoningToken:
		if s.mode == ladr.TraceModeOff {
			return ""
		}
		if event.Text == "" {
			return ""
		}
		if s.mode == ladr.TraceModeInline {
			if !s.inlineThinkOpen {
				s.inlineThinkOpen = true
				return s.chunk(map[string]any{"content": "<think>\n" + event.Text}, "")
			}
			return s.chunk(map[string]any{"content": event.Text}, "")
		}
		return s.chunk(map[string]any{"reasoning_content": event.Text}, "")

	case trace.EventAnswerToken:
		if event.Text == "" {
			return ""
		}
		if s.mode == ladr.TraceModeInline && s.inlineThinkOpen {
			s.inlineThinkOpen = false
			return s.chunk(map[string]any{"content": "</think>\n\n" + event.Text}, "")
		}
		return s.chunk(map[string]any{"content": event.Text}, "")

	case trace.EventAnswerComplete, trace.EventDone:
		return ""
	}

	return ""
}

func (s *streamShaper) Finalize(status string, usage *Usage) string {
	if s.finished {
		return ""
	}
	s.finished = true

	if status == "error" {
		errPayload := OpenAIError("Ladder execution failed.", "api_error", "ladder_execution_error")
		out := sseJSON(map[string]any{
			"id":      s.sessionID,
			"object":  "chat.completion.chunk",
			"created": s.created,
			"model":   s.model,
			"choices": []map[string]any{
				{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"},
			},
		})
		out += sseJSON(errPayload)
		out += "data: [DONE]\n\n"
		return out
	}

	var out string
	if s.mode == ladr.TraceModeInline && s.inlineThinkOpen {
		s.inlineThinkOpen = false
		out += s.chunk(map[string]any{"content": "\n</think>"}, "")
	}
	out += s.chunk(map[string]any{}, "stop")

	if usage != nil {
		out += sseJSON(map[string]any{
			"id":      s.sessionID,
			"object":  "chat.completion.chunk",
			"created": s.created,
			"model":   s.model,
			"choices": []map[string]any{},
			"usage": map[string]any{
				"prompt_tokens":     usage.PromptTokens,
				"completion_tokens": usage.CompletionTokens,
				"total_tokens":      usage.PromptTokens + usage.CompletionTokens,
			},
		})
	}
	out += "data: [DONE]\n\n"
	return out
}

// --- Non-streaming response ---

func renderReasoningText(snapshot *trace.TraceSnapshot) string {
	var blockParts []string
	for _, block := range snapshot.Blocks {
		var nodeParts []string
		for _, node := range block.Nodes {
			nodeParts = append(nodeParts, strings.TrimSpace("### "+node.Heading+"\n"+node.Text))
		}
		blockParts = append(blockParts, strings.Join(nodeParts, "\n\n"))
	}
	return strings.TrimSpace(strings.Join(blockParts, "\n\n"))
}

// AssembleChatCompletion builds the non-streaming OpenAI response from a trace snapshot.
func AssembleChatCompletion(snapshot *trace.TraceSnapshot, opts ChatCompletionOptions) map[string]any {
	answer := ""
	if snapshot.Answer != nil {
		answer = *snapshot.Answer
	}
	created := ts(opts.Created)

	usageObj := map[string]any{
		"prompt_tokens":     0,
		"completion_tokens": 0,
		"total_tokens":      0,
	}
	if opts.Usage != nil {
		usageObj["prompt_tokens"] = opts.Usage.PromptTokens
		usageObj["completion_tokens"] = opts.Usage.CompletionTokens
		usageObj["total_tokens"] = opts.Usage.PromptTokens + opts.Usage.CompletionTokens
	}

	if opts.Mode == ladr.TraceModeInline {
		reasoning := renderReasoningText(snapshot)
		content := answer
		if reasoning != "" {
			content = "<think>\n" + reasoning + "\n</think>\n\n" + answer
		}
		return map[string]any{
			"id":      opts.SessionID,
			"object":  "chat.completion",
			"created": created,
			"model":   opts.Model,
			"choices": []map[string]any{
				{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": content},
					"finish_reason": "stop",
				},
			},
			"usage": usageObj,
		}
	}

	message := map[string]any{"role": "assistant", "content": answer}
	if opts.Mode != ladr.TraceModeOff {
		reasoning := renderReasoningText(snapshot)
		if reasoning != "" {
			message["reasoning_content"] = reasoning
		}
	}

	return map[string]any{
		"id":      opts.SessionID,
		"object":  "chat.completion",
		"created": created,
		"model":   opts.Model,
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       message,
				"finish_reason": "stop",
			},
		},
		"usage": usageObj,
	}
}

// ListModelsResponse builds the /v1/models response from ladder entries.
func ListModelsResponse(entries []LadderModelEntry) map[string]any {
	data := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		data = append(data, map[string]any{
			"id":       entry.ID,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": entry.Author,
		})
	}
	return map[string]any{
		"object": "list",
		"data":   data,
	}
}
