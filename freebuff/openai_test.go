package freebuff

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeChatMessagesInjectsBuffy(t *testing.T) {
	msgs := []any{
		map[string]any{"role": "user", "content": "hi"},
	}
	out := NormalizeChatMessages(msgs)
	if len(out) < 2 {
		t.Fatalf("expected system+user, got %d", len(out))
	}
	if out[0]["role"] != "system" {
		t.Fatalf("first role: %v", out[0]["role"])
	}
	content, _ := out[0]["content"].(string)
	if !strings.HasPrefix(content, "You are Buffy") {
		t.Fatalf("missing buffy prefix: %q", content)
	}
}

func TestNormalizeDeveloperRole(t *testing.T) {
	msgs := []any{
		map[string]any{"role": "developer", "content": "rules"},
		map[string]any{"role": "user", "content": "hi"},
	}
	out := NormalizeChatMessages(msgs)
	if out[0]["role"] != "system" {
		t.Fatalf("developer should become system, got %v", out[0]["role"])
	}
	content, _ := out[0]["content"].(string)
	if !strings.Contains(content, "rules") {
		t.Fatalf("expected original content kept: %q", content)
	}
}

func TestFilterFunctionTools(t *testing.T) {
	tools := []any{
		map[string]any{"type": "function", "function": map[string]any{"name": "a"}},
		map[string]any{"type": "custom", "name": "b"},
		map[string]any{"function": map[string]any{"name": "c"}}, // empty type treated as function
	}
	out := FilterFunctionTools(tools).([]any)
	if len(out) != 2 {
		t.Fatalf("expected 2 tools, got %d: %#v", len(out), out)
	}
}

func TestBuildUpstreamPayload(t *testing.T) {
	body := map[string]any{
		"model": "deepseek/deepseek-v4-flash",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"temperature": 0.2,
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "x"}},
			map[string]any{"type": "custom"},
		},
	}
	session := Session{InstanceID: "inst-1", Model: "mimo/mimo-v2.5"}
	payload := BuildUpstreamPayload(body, session, "run-1", "client-1", "trace-1", "mimo/mimo-v2.5")
	if payload["model"] != "mimo/mimo-v2.5" {
		t.Fatalf("upstream model: %v", payload["model"])
	}
	if payload["stream"] != true {
		t.Fatalf("stream must be true")
	}
	meta, _ := payload["codebuff_metadata"].(map[string]any)
	if meta["freebuff_instance_id"] != "inst-1" || meta["run_id"] != "run-1" {
		t.Fatalf("metadata: %#v", meta)
	}
	tools, _ := payload["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools filtered: %#v", tools)
	}
}

func TestCompletionAccumulator(t *testing.T) {
	acc := NewCompletionAccumulator("test-model")
	acc.Add(map[string]any{
		"id": "chatcmpl-1",
		"choices": []any{
			map[string]any{
				"delta": map[string]any{"content": "Hel", "reasoning_content": "think"},
			},
		},
	})
	acc.Add(map[string]any{
		"choices": []any{
			map[string]any{
				"delta":         map[string]any{"content": "lo"},
				"finish_reason": "stop",
			},
		},
	})
	resp := acc.FinalResponse()
	msg := resp["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "Hello" {
		t.Fatalf("content=%v", msg["content"])
	}
	if msg["reasoning_content"] != "think" {
		t.Fatalf("reasoning=%v", msg["reasoning_content"])
	}
	raw, _ := json.Marshal(resp)
	if !strings.Contains(string(raw), "chat.completion") {
		t.Fatalf("bad response: %s", raw)
	}
}

func TestResolveModelSuffix(t *testing.T) {
	m, ok := ResolveModel("deepseek-v4-flash", AllModels)
	if !ok || m.ID != "deepseek/deepseek-v4-flash" {
		t.Fatalf("resolve suffix failed: ok=%v m=%+v", ok, m)
	}
	m, ok = ResolveModel("google/gemini-2.5-flash-lite", AllModels)
	if !ok || m.UpstreamID() != GeminiUpstreamID || m.SessionID() != GeminiUpstreamID {
		t.Fatalf("gemini alias: %+v", m)
	}
}

func TestAuthStorageTokenList(t *testing.T) {
	sa, err := ParseAuthStorage([]byte(`{"token":"a, b","tokens":["b","c"]}`))
	if err != nil {
		t.Fatal(err)
	}
	tokens := sa.TokenList()
	if len(tokens) != 3 {
		t.Fatalf("tokens=%v", tokens)
	}
}

func TestAgentValidationPayload(t *testing.T) {
	payload := AgentValidationPayload(AllModels)
	defs, ok := payload["agentDefinitions"].([]map[string]any)
	if !ok || len(defs) == 0 {
		// type may be []map after append - check via json
		raw, _ := json.Marshal(payload)
		if !strings.Contains(string(raw), "agentDefinitions") {
			t.Fatalf("payload: %s", raw)
		}
	}
}
