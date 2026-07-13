package freebuff

import "testing"

func TestMapModelToAgentID(t *testing.T) {
	if got := MapModelToAgentID("deepseek/deepseek-v4-flash"); got != "base2-free-deepseek-flash" {
		t.Fatalf("got %s", got)
	}
	if got := MapModelToAgentID("z-ai/glm-5.2"); got != "base2-free" {
		t.Fatalf("got %s", got)
	}
	if got := MapModelToAgentID("google/gemini-3.1-pro-preview"); got != GeminiAgentID {
		t.Fatalf("got %s", got)
	}
}

func TestDeriveDisplayNameAliases(t *testing.T) {
	if got := DeriveDisplayName("tencent/hy3"); got != "Hunyuan 3" {
		t.Fatalf("got %s", got)
	}
	if got := DeriveDisplayName("z-ai/glm-5.2"); got != "GLM 5.2" {
		t.Fatalf("got %s", got)
	}
}
