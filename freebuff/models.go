package freebuff

import "strings"

// Model describes a Freebuff-facing model and its Codebuff agent mapping.
type Model struct {
	ID              string
	AgentID         string
	OwnedBy         string
	UpstreamModelID string
	SessionModelID  string
	ParentAgentID   string
	DisplayName     string
}

// UpstreamID is the model id sent in the chat payload.
func (m Model) UpstreamID() string {
	if m.UpstreamModelID != "" {
		return m.UpstreamModelID
	}
	return m.ID
}

// SessionID is the model id used when creating/binding Freebuff sessions.
func (m Model) SessionID() string {
	if m.SessionModelID != "" {
		return m.SessionModelID
	}
	return m.UpstreamID()
}

const (
	ContextPrunerAgentID = "context-pruner"
	GeminiAgentID        = "base2-free-mimo"
	GeminiUpstreamID     = "mimo/mimo-v2.5"
)

// Static models mirrored from the Python freebuff2api registry.
var FreebuffModels = []Model{
	{ID: "deepseek/deepseek-v4-flash", AgentID: "base2-free-deepseek-flash", DisplayName: "DeepSeek V4 Flash"},
	{ID: "deepseek/deepseek-v4-pro", AgentID: "base2-free-deepseek", DisplayName: "DeepSeek V4 Pro"},
	{ID: "moonshotai/kimi-k2.7-code", AgentID: "base2-free-kimi", DisplayName: "Kimi K2.7 Code"},
	{ID: "minimax/minimax-m3", AgentID: "base2-free-minimax-m3", DisplayName: "MiniMax M3"},
	{ID: "mimo/mimo-v2.5", AgentID: "base2-free-mimo", DisplayName: "MiMo 2.5"},
	{ID: "mimo/mimo-v2.5-pro", AgentID: "base2-free-mimo-pro", DisplayName: "MiMo 2.5 Pro"},
	{ID: "kwaipilot/kat-coder-pro-v2", AgentID: "base2-free", DisplayName: "KAT Coder Pro V2"},
	{ID: "z-ai/glm-5.2", AgentID: "base2-free", DisplayName: "GLM 5.2"},
	{ID: "tencent/hy3:free", AgentID: "base2-free", DisplayName: "Hunyuan 3"},
}

// Gemini free labels are thin aliases onto MiMo 2.5 (same quota / agent pool).
var GeminiFreeModels = []Model{
	{
		ID: "google/gemini-2.5-flash-lite", AgentID: GeminiAgentID, OwnedBy: "google",
		UpstreamModelID: GeminiUpstreamID, SessionModelID: GeminiUpstreamID,
		DisplayName: "Gemini 2.5 Flash Lite",
	},
	{
		ID: "google/gemini-3.1-flash-lite-preview", AgentID: GeminiAgentID, OwnedBy: "google",
		UpstreamModelID: GeminiUpstreamID, SessionModelID: GeminiUpstreamID,
		DisplayName: "Gemini 3.1 Flash Lite Preview",
	},
	{
		ID: "google/gemini-3.1-pro-preview", AgentID: GeminiAgentID, OwnedBy: "google",
		UpstreamModelID: GeminiUpstreamID, SessionModelID: GeminiUpstreamID,
		DisplayName: "Gemini 3.1 Pro Preview",
	},
}

// AllModels is the hardcoded registry used when upstream discovery is empty.
var AllModels = append(append([]Model{}, FreebuffModels...), GeminiFreeModels...)

var providerAgentMap = map[string]string{
	"deepseek/":   "base2-free-deepseek",
	"moonshotai/": "base2-free-kimi",
	"minimax/":    "base2-free",
	"mimo/":       "base2-free-mimo",
	"tencent/":    "base2-free",
	"kwaipilot/":  "base2-free",
	"z-ai/":       "base2-free",
	"google/":     GeminiAgentID,
}

var displayNameAliases = map[string]string{
	"z-ai/glm-5.2":     "GLM 5.2",
	"tencent/hy3":      "Hunyuan 3",
	"tencent/hy3:free": "Hunyuan 3 Free",
	"tencent/hy3.free": "Hunyuan 3 Free",
}

// MapModelToAgentID maps an upstream model id to a Codebuff agent id.
func MapModelToAgentID(modelID string) string {
	for _, m := range AllModels {
		if m.ID == modelID {
			return m.AgentID
		}
	}
	for prefix, agentID := range providerAgentMap {
		if strings.HasPrefix(modelID, prefix) {
			return agentID
		}
	}
	return modelID
}

// DeriveDisplayName builds a human-readable name from a model id.
func DeriveDisplayName(modelID string) string {
	if name, ok := displayNameAliases[modelID]; ok {
		return name
	}
	display := modelID
	if i := strings.LastIndex(display, "/"); i >= 0 {
		display = display[i+1:]
	}
	display = strings.ReplaceAll(display, "-", " ")
	display = strings.ReplaceAll(display, ":", " ")
	return simpleTitle(display)
}

func simpleTitle(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		if p == "" {
			continue
		}
		lower := strings.ToLower(p)
		parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(parts, " ")
}

// ResolveModel finds a model by exact id or bare suffix (clients often omit provider/).
func ResolveModel(requested string, active []Model) (Model, bool) {
	if len(active) == 0 {
		active = AllModels
	}
	if requested == "" {
		return active[0], true
	}
	for _, m := range active {
		if m.ID == requested {
			return m, true
		}
	}
	suffix := "/" + requested
	for _, m := range active {
		if strings.HasSuffix(m.ID, suffix) {
			return m, true
		}
	}
	// Accept bare Gemini / MiMo aliases that map onto the alias table.
	for _, m := range AllModels {
		if m.ID == requested || strings.HasSuffix(m.ID, suffix) {
			return m, true
		}
	}
	return Model{}, false
}

// AgentValidationPayload builds the body for POST /api/agents/validate.
func AgentValidationPayload(active []Model) map[string]any {
	if len(active) == 0 {
		active = AllModels
	}
	modelsByAgent := map[string]Model{}
	spawnableByAgent := map[string]map[string]struct{}{}
	for _, model := range active {
		modelsByAgent[model.AgentID] = model
		if spawnableByAgent[model.AgentID] == nil {
			spawnableByAgent[model.AgentID] = map[string]struct{}{}
		}
		spawnableByAgent[model.AgentID][ContextPrunerAgentID] = struct{}{}
		if model.ParentAgentID != "" {
			if spawnableByAgent[model.ParentAgentID] == nil {
				spawnableByAgent[model.ParentAgentID] = map[string]struct{}{}
			}
			spawnableByAgent[model.ParentAgentID][model.AgentID] = struct{}{}
		}
	}
	definitions := make([]map[string]any, 0, len(modelsByAgent)+1)
	for agentID, model := range modelsByAgent {
		spawnable := sortedKeys(spawnableByAgent[agentID])
		definitions = append(definitions, agentDefinition(
			agentID,
			model.UpstreamID(),
			"Freebuff "+model.UpstreamID(),
			spawnable,
		))
	}
	definitions = append(definitions, agentDefinition(
		ContextPrunerAgentID,
		active[0].ID,
		"Context Pruner",
		nil,
	))
	return map[string]any{"agentDefinitions": definitions}
}

func agentDefinition(agentID, modelID, displayName string, spawnable []string) map[string]any {
	toolNames := []string{}
	if len(spawnable) > 0 {
		toolNames = []string{"spawn_agents"}
	}
	if spawnable == nil {
		spawnable = []string{}
	}
	return map[string]any{
		"id":            agentID,
		"publisher":     "codebuff",
		"model":         modelID,
		"displayName":   displayName,
		"spawnerPrompt": "Freebuff OpenAI-compatible orchestrator",
		"inputSchema": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "A coding task to complete",
			},
			"params": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}},
		},
		"outputMode":            "last_message",
		"includeMessageHistory": true,
		"toolNames":             toolNames,
		"spawnableAgents":       spawnable,
		"systemPrompt":          "Act as a helpful coding assistant.",
	}
}

func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	// tiny stable order without importing sort-heavy path in hot loops elsewhere
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
