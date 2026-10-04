package provider

import (
	"testing"
)

func TestAnthropicAdapter_IncludesAssistantMessages(t *testing.T) {
	a := &anthropicAdapter{}
	messages := []Message{
		{Role: RoleSystem, ContentStr: "You are helpful."},
		{Role: RoleUser, ContentStr: "Hello"},
		{Role: RoleAssistant, ContentStr: "Hi there!"},
		{Role: RoleUser, ContentStr: "What is 2+2?"},
	}

	payload := a.buildAnthropicPayload("claude-3-opus", messages, ProviderConfig{}, false)

	apiMessages, ok := payload["messages"].([]map[string]interface{})
	if !ok {
		t.Fatal("expected messages to be a slice of maps")
	}

	if len(apiMessages) != 3 {
		t.Fatalf("expected 3 conversation messages (user+assistant+user), got %d", len(apiMessages))
	}

	if apiMessages[0]["role"] != "user" {
		t.Errorf("expected first message role 'user', got %v", apiMessages[0]["role"])
	}
	if apiMessages[1]["role"] != "assistant" {
		t.Errorf("expected second message role 'assistant', got %v", apiMessages[1]["role"])
	}
	if apiMessages[2]["role"] != "user" {
		t.Errorf("expected third message role 'user', got %v", apiMessages[2]["role"])
	}

	if payload["system"] != "You are helpful." {
		t.Errorf("expected system prompt, got %v", payload["system"])
	}
}

func TestAnthropicAdapter_OnlySystemMessages(t *testing.T) {
	a := &anthropicAdapter{}
	messages := []Message{
		{Role: RoleSystem, ContentStr: "System prompt"},
	}

	payload := a.buildAnthropicPayload("claude-3-opus", messages, ProviderConfig{}, false)

	apiMessages, ok := payload["messages"].([]map[string]interface{})
	if !ok {
		t.Fatal("expected messages to be a slice of maps")
	}

	if len(apiMessages) != 1 {
		t.Fatalf("expected 1 fallback message, got %d", len(apiMessages))
	}
}

func TestAnthropicAdapter_AssistantContentParts(t *testing.T) {
	a := &anthropicAdapter{}
	messages := []Message{
		{Role: RoleUser, ContentStr: "Hello"},
		{
			Role: RoleAssistant,
			ContentParts: []MessagePart{
				{Type: "text", Text: "Hi!"},
			},
		},
	}

	payload := a.buildAnthropicPayload("claude-3-opus", messages, ProviderConfig{}, false)

	apiMessages := payload["messages"].([]map[string]interface{})
	if len(apiMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(apiMessages))
	}

	content, ok := apiMessages[1]["content"].([]map[string]interface{})
	if !ok {
		t.Fatal("expected assistant content to be a slice of maps")
	}
	if len(content) != 1 || content[0]["text"] != "Hi!" {
		t.Errorf("unexpected assistant content: %v", content)
	}
}

func TestOpenAiAdapter_IncludesAssistantMessages(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, ContentStr: "System"},
		{Role: RoleUser, ContentStr: "Hello"},
		{Role: RoleAssistant, ContentStr: "Hi!"},
		{Role: RoleUser, ContentStr: "Bye"},
	}

	result := toOpenAiMessages(messages)

	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
	if result[0]["role"] != "system" {
		t.Errorf("expected system, got %v", result[0]["role"])
	}
	if result[1]["role"] != "user" {
		t.Errorf("expected user, got %v", result[1]["role"])
	}
	if result[2]["role"] != "assistant" {
		t.Errorf("expected assistant, got %v", result[2]["role"])
	}
	if result[3]["role"] != "user" {
		t.Errorf("expected user, got %v", result[3]["role"])
	}
}
