package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Issue #1: unit-test framing must instruct the model to answer normally
// when the input is not a tool call (clients like NewAPI/Cherry attach tools
// to ordinary chat). Otherwise the model fabricates
// "Query ... does not match any valid API endpoint for unit testing".
func TestBuildToolsSystemPromptHasNoToolFallback(t *testing.T) {
	tools := []json.RawMessage{
		json.RawMessage(`{"type":"function","function":{"name":"write_file","description":"Write content to a file","parameters":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}}}`),
	}
	p := buildToolsSystemPrompt(tools)

	if !strings.Contains(p, "write_file") {
		t.Fatalf("prompt should list the tool, got: %s", p)
	}
	if !strings.Contains(p, "do NOT output JSON") {
		t.Fatalf("prompt must tell the model not to output JSON for non-tool input, got: %s", p)
	}
	if !strings.Contains(p, "Respond to the input normally") {
		t.Fatalf("prompt must tell the model to answer normally, got: %s", p)
	}
	// The issue's exact symptom must be addressed in wording.
	if !strings.Contains(p, "do NOT invent an error") {
		t.Fatalf("prompt must forbid inventing errors, got: %s", p)
	}
}

// Issue #1: OpenAI spec — stream defaults to false when omitted.
// Clients that omit it expect a plain JSON response, not SSE.
func TestStreamEnabledDefaultsFalse(t *testing.T) {
	if got := (&chatRequest{}).streamEnabled(); got != false {
		t.Fatalf("stream omitted: want false, got %v", got)
	}
	tru, fals := true, false
	if got := (&chatRequest{Stream: &tru}).streamEnabled(); got != true {
		t.Fatalf("stream=true: want true, got %v", got)
	}
	if got := (&chatRequest{Stream: &fals}).streamEnabled(); got != false {
		t.Fatalf("stream=false: want false, got %v", got)
	}
}

// Issue #1: a model reply that is a bare JSON tool-call with an unknown name
// ("text") must NOT be forwarded to the client as-is — it is the exact garbage
// the user saw wrapped in {"name":"text","arguments":{...}}.
func TestParseToolCallsDropsUnknownToolName(t *testing.T) {
	tools := []json.RawMessage{
		json.RawMessage(`{"type":"function","function":{"name":"write_file","parameters":{"type":"object","properties":{}}}}`),
	}
	content := `{"name": "text", "arguments": {"text": "{\"error\": \"Invalid request\", \"message\": \"Query 'what are you model' does not match any valid API endpoint for unit testing.\"}"}}`
	calls := parseToolCalls(content, tools)
	if len(calls) != 0 {
		t.Fatalf("unknown tool name should be dropped, got %d calls: %+v", len(calls), calls)
	}
}
