package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Issue #1 regression: model mixes narration with a tool-call JSON whose
// argument string contains unescaped double quotes (real case from logs:
// content was `已推送。现在去 issue 回复：\n{"name":"terminal","arguments":
// {"command":"... --body "## ...""}}`). The old parser dropped it and leaked
// the raw JSON as text — the exact "agent won't do work" symptom.
func TestParseEmbeddedJSONRepairsUnescapedQuotes(t *testing.T) {
	content := "已推送。现在去 issue 回复：\n" +
		`{"name":"terminal","arguments":{"command":"cd /root/zai2api && gh issue comment 1 --repo pingmike2/zai2api --body "## 已修复""}}`
	tools := []json.RawMessage{
		json.RawMessage(`{"type":"function","function":{"name":"terminal","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}`),
	}
	calls := parseToolCalls(content, tools)
	if len(calls) != 1 {
		t.Fatalf("want 1 tool call, got %d (content=%s)", len(calls), content)
	}
	if calls[0].Function.Name != "terminal" {
		t.Fatalf("want name=terminal, got %s", calls[0].Function.Name)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("repaired arguments not valid JSON: %v (%s)", err, calls[0].Function.Arguments)
	}
	if !strings.Contains(args["command"], "--body") {
		t.Fatalf("command arg mangled: %s", args["command"])
	}
}

func TestRepairUnescapedQuotes(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"a":"x"}`, `{"a":"x"}`},
		{`{"a":"say "hi" now"}`, `{"a":"say \"hi\" now"}`},
		{`{"a":"--body "## text""}`, `{"a":"--body \"## text\""}`},
		{`{"a":"done","b":1}`, `{"a":"done","b":1}`},
	}
	for _, c := range cases {
		if got := repairUnescapedQuotes(c.in); got != c.want {
			t.Errorf("repair(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if !json.Valid([]byte(repairUnescapedQuotes(`{"a":"--body "## x""}`))) {
		t.Error("repaired output should be valid JSON")
	}
}

// Issue #1: system messages must be preserved — the old code dropped them,
// which made the model lose all identity/context ("no context" symptom).
func TestConvertToolMessagesKeepsSystemContext(t *testing.T) {
	sys := json.RawMessage(`{"role":"system","content":"You are Hermes Agent, built by Nous Research."}`)
	user := json.RawMessage(`{"role":"user","content":"你好"}`)
	tools := []json.RawMessage{
		json.RawMessage(`{"type":"function","function":{"name":"terminal","parameters":{"type":"object","properties":{}}}}`),
	}
	out := convertToolMessages([]json.RawMessage{sys, user}, tools)
	var sysFound, userFound bool
	for _, raw := range out {
		var m map[string]string
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if m["role"] == "system" && strings.Contains(m["content"], "Hermes Agent") {
			sysFound = true
		}
		if m["role"] == "user" && strings.Contains(m["content"], "User request: 你好") {
			userFound = true
		}
	}
	if !sysFound {
		t.Fatal("system message dropped — model loses identity/context")
	}
	if !userFound {
		t.Fatal("user message not embedded with framing")
	}
}

// stripFraming must not mangle a normal reply.
func TestStripFraming(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Available functions:\n- terminal\n\nHello there", "Hello there"},
		{"You are an AI assistant with access to functions.\nUser request: hi", "hi"},
		{"plain answer", "plain answer"},
	}
	for _, c := range cases {
		if got := strings.TrimSpace(stripFraming(c.in)); got != c.want {
			t.Errorf("stripFraming(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

