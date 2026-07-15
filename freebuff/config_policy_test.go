package freebuff

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeWritesDisabledAndExcluded(t *testing.T) {
	sa := AuthStorage{
		Token:          "tok",
		LoginMode:      "freebuff",
		Disabled:       true,
		ExcludedModels: []string{"deepseek/deepseek-v4-pro", "minimax-m3"},
		ModelAliases: []ModelAlias{
			{Name: "deepseek/deepseek-v4-flash", Alias: "ds-flash"},
		},
		Prefix:   "fb",
		Priority: 10,
	}
	sa.Normalize()
	if sa.Metadata["disabled"] != true {
		t.Fatalf("metadata disabled: %#v", sa.Metadata["disabled"])
	}
	if sa.Attributes["disabled"] != "true" {
		t.Fatalf("attrs disabled=%s", sa.Attributes["disabled"])
	}
	if sa.Attributes["auth_kind"] != "oauth" {
		t.Fatalf("auth_kind=%s", sa.Attributes["auth_kind"])
	}
	if !strings.Contains(sa.Attributes["excluded_models"], "deepseek") {
		t.Fatalf("excluded attrs=%s", sa.Attributes["excluded_models"])
	}
	if sa.Attributes["model_aliases"] == "" {
		t.Fatal("model_aliases attr empty")
	}
	var aliases []ModelAlias
	if err := json.Unmarshal([]byte(sa.Attributes["model_aliases"]), &aliases); err != nil || len(aliases) != 1 {
		t.Fatalf("aliases: %v %v", aliases, err)
	}
}

func TestApplyHostCredentialFieldsFromMetadata(t *testing.T) {
	sa := AuthStorage{Token: "t", LoginMode: "freebuff"}
	ApplyHostCredentialFields(&sa, map[string]any{
		"disabled":        true,
		"excluded_models": []any{"mimo/mimo-v2.5", "kimi"},
		"model_aliases": []any{
			map[string]any{"name": "deepseek/deepseek-v4-flash", "alias": "flash"},
		},
		"prefix":    "x",
		"proxy_url": "http://127.0.0.1:1",
		"priority":  float64(3),
	}, nil)
	if !sa.Disabled {
		t.Fatal("disabled")
	}
	if sa.Prefix != "x" || sa.ProxyURL == "" || sa.Priority != 3 {
		t.Fatalf("%+v", sa)
	}
	if len(sa.ExcludedModels) != 2 {
		t.Fatalf("excluded=%v", sa.ExcludedModels)
	}
	if len(sa.ModelAliases) != 1 || sa.ModelAliases[0].Alias != "flash" {
		t.Fatalf("aliases=%+v", sa.ModelAliases)
	}
}

func TestEnsureModelAllowed(t *testing.T) {
	sa := AuthStorage{
		ExcludedModels: []string{"deepseek/deepseek-v4-pro", "minimax-m3"},
		Prefix:         "fb",
	}
	if err := EnsureModelAllowed("deepseek/deepseek-v4-pro", sa); err == nil {
		t.Fatal("expected exclude full id")
	}
	if err := EnsureModelAllowed("minimax-m3", sa); err == nil {
		t.Fatal("expected exclude bare")
	}
	if err := EnsureModelAllowed("deepseek/deepseek-v4-flash", sa); err != nil {
		t.Fatal(err)
	}
}

func TestFilterModelsByExcluded(t *testing.T) {
	all := []Model{
		{ID: "deepseek/deepseek-v4-flash"},
		{ID: "deepseek/deepseek-v4-pro"},
		{ID: "mimo/mimo-v2.5"},
	}
	out := FilterModelsByExcluded(all, []string{"deepseek-v4-pro"})
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	for _, m := range out {
		if strings.Contains(m.ID, "pro") {
			t.Fatalf("still has pro: %s", m.ID)
		}
	}
}

func TestParseAuthStorageExcludedHyphen(t *testing.T) {
	raw := []byte(`{
		"token":"abc",
		"excluded-models":["a","b"],
		"model-aliases":[{"name":"x","alias":"y"}]
	}`)
	sa, err := ParseAuthStorage(raw)
	if err != nil {
		t.Fatal(err)
	}
	sa.Normalize()
	if len(sa.ExcludedModels) != 2 {
		t.Fatalf("excluded=%v", sa.ExcludedModels)
	}
	if len(sa.ModelAliases) != 1 {
		t.Fatalf("aliases=%v", sa.ModelAliases)
	}
}
