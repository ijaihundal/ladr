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

// Package provider implements the provider-adapters system, ported from
// packages/provider-adapters (TypeScript).  It provides the types, catalog,
// resolver, registry, and per-provider adapters for calling LLM APIs.
package provider

import "context"

// --- Message types ---

// MessageRole enumerates the roles a chat message can have.
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
)

// FilePartData carries optional file metadata for file-type message parts.
type FilePartData struct {
	URL      *string
	MimeType *string
	Data     *string
	Filename *string
	FileID   *string
}

// MessagePart represents one part of a multi-part message content.
// Exactly one of the type-specific fields should be set based on Type.
type MessagePart struct {
	Type     string // "text" | "image_url" | "file"
	Text     string
	ImageURL *struct {
		URL    string
		Detail string // "auto" | "low" | "high" | ""
	}
	File *FilePartData
}

// Message is a single chat message with a role and content.
// Content can be either a plain string or a slice of MessagePart.
type Message struct {
	Role         MessageRole
	ContentStr   string
	ContentParts []MessagePart
}

// ContentString returns the content as a string, joining parts if needed.
func (m Message) ContentString() string {
	if m.ContentStr != "" {
		return m.ContentStr
	}
	var sb []string
	for _, p := range m.ContentParts {
		switch p.Type {
		case "text":
			sb = append(sb, p.Text)
		case "image_url":
			sb = append(sb, "[image] "+p.ImageURL.URL)
		case "file":
			if p.File != nil {
				if p.File.URL != nil {
					sb = append(sb, "[file] "+*p.File.URL)
				} else if p.File.Filename != nil {
					sb = append(sb, "[file] "+*p.File.Filename)
				} else {
					sb = append(sb, "[file] ")
				}
			}
		}
	}
	if len(sb) == 0 {
		return ""
	}
	result := sb[0]
	for _, s := range sb[1:] {
		result += "\n" + s
	}
	return result
}

// --- Provider config ---

// ProviderConfig holds all parameters for a single provider call.
type ProviderConfig struct {
	Provider    string
	Model       string
	APIKey      string
	BaseURL     string
	Temperature *float64
	MaxTokens   *int
	TimeoutMs   int
}

// --- Token usage / result ---

// TokenUsage reports token counts for a provider call.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// AdapterResult is the non-streaming response from a provider call.
type AdapterResult struct {
	Text  string
	Usage *TokenUsage
}

// --- Stream events ---

// StreamEventType identifies the kind of stream event.
type StreamEventType string

const (
	StreamTextDelta StreamEventType = "text_delta"
	StreamUsage     StreamEventType = "usage"
	StreamDone      StreamEventType = "done"
	StreamError     StreamEventType = "error"
)

// StreamEvent is a discriminated union for streaming events.
type StreamEvent struct {
	Type  StreamEventType
	Text  string      // valid when Type == StreamTextDelta
	Usage *TokenUsage // valid when Type == StreamUsage
}

// NewTextDelta creates a text_delta stream event.
func NewTextDelta(text string) StreamEvent {
	return StreamEvent{Type: StreamTextDelta, Text: text}
}

// NewUsageEvent creates a usage stream event.
func NewUsageEvent(u TokenUsage) StreamEvent {
	return StreamEvent{Type: StreamUsage, Usage: &u}
}

// NewDone creates a done stream event.
func NewDone() StreamEvent {
	return StreamEvent{Type: StreamDone}
}

// --- Provider adapter interface ---

// ProviderAdapter is the interface every provider adapter implements.
type ProviderAdapter interface {
	Name() string
	Call(ctx context.Context, model string, messages []Message, config ProviderConfig) (*AdapterResult, error)
	Stream(ctx context.Context, model string, messages []Message, config ProviderConfig) (<-chan StreamEvent, error)
}

// --- Provider connection / policy / chain ---

// ProviderConnection describes a user's configured provider connection.
type ProviderConnection struct {
	Provider     string
	EncryptedKey string
	APIKey       string
	Priority     int
	BaseURL      string
	Label        *string
	IsCustom     bool
}

// ProviderPolicy controls fallback and backoff behaviour.
type ProviderPolicy struct {
	FallbackEnabled       bool
	MaxAttempts           int
	SingleProviderBackoff bool
	BackoffBaseMs         int
	BackoffMaxMs          int
}

// AllowedTarget is a provider+model pair that a model is allowed to route to.
type AllowedTarget struct {
	Provider string
	Model    string
}

// AllowedTargetsPolicy controls which providers/models are permitted.
// Strategy is either "universal" or "constrained".
type AllowedTargetsPolicy struct {
	Strategy  string   // "universal" | "constrained"
	Providers []string // used when Strategy == "constrained"
	Models    []string // used when Strategy == "constrained"
}

// ProviderChain is the resolved set of attempts to try for a single model call.
type ProviderChain struct {
	Attempts                 []ProviderConfig
	MaxAttempts              int
	UseSingleProviderBackoff bool
	BackoffBaseMs            int
	BackoffMaxMs             int
}
