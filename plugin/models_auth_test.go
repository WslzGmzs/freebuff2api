package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WslzGmzs/freebuff2api/freebuff"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestRegisterUsesOAuthModelScope(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	raw, err := d.Handle(pluginabi.MethodPluginRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	var reg Registration
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatal(err)
	}
	if reg.Capabilities.ExecutorModelScope != pluginapi.ExecutorModelScopeOAuth {
		t.Fatalf("scope=%v want oauth", reg.Capabilities.ExecutorModelScope)
	}
}

func TestModelStaticEmpty(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	raw, err := d.Handle(pluginabi.MethodModelStatic, nil)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	var resp pluginapi.ModelResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Models) != 0 {
		t.Fatalf("static models should be empty, got %d", len(resp.Models))
	}
}

func TestModelForAuthDisabledEmpty(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	payload, _ := json.Marshal(map[string]any{
		"StorageJSON": []byte(`{"token":"t","disabled":true}`),
	})
	raw, err := d.Handle(pluginabi.MethodModelForAuth, payload)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	var resp pluginapi.ModelResponse
	_ = json.Unmarshal(env.Result, &resp)
	if len(resp.Models) != 0 {
		t.Fatalf("disabled auth should list no models, got %d", len(resp.Models))
	}
}

func TestModelForAuthEnabledListsModels(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	payload, _ := json.Marshal(map[string]any{
		"StorageJSON": []byte(`{"token":"t","disabled":false}`),
	})
	raw, err := d.Handle(pluginabi.MethodModelForAuth, payload)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	var resp pluginapi.ModelResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Models) < 3 {
		t.Fatalf("expected models, got %d", len(resp.Models))
	}
}

func TestModelForAuthExcludedFilters(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	// Exclude first hardcoded freebuff model id if present.
	first := freebuff.AllModels[0].ID
	storage, _ := json.Marshal(map[string]any{
		"token":           "t",
		"excluded_models": []string{first},
	})
	payload, _ := json.Marshal(map[string]any{"StorageJSON": storage})
	raw, err := d.Handle(pluginabi.MethodModelForAuth, payload)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	var resp pluginapi.ModelResponse
	_ = json.Unmarshal(env.Result, &resp)
	for _, m := range resp.Models {
		if m.ID == first {
			t.Fatalf("excluded model still listed: %s", first)
		}
	}
}

func TestModelForAuthEmptyStorage(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	raw, err := d.Handle(pluginabi.MethodModelForAuth, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	var resp pluginapi.ModelResponse
	_ = json.Unmarshal(env.Result, &resp)
	if len(resp.Models) != 0 {
		t.Fatalf("no storage → no models, got %d", len(resp.Models))
	}
}

func TestToAuthDataExportsExcludedAndAliases(t *testing.T) {
	ad := ToAuthData(freebuff.AuthStorage{
		Token:          "t",
		LoginMode:      "freebuff",
		Disabled:       true,
		ExcludedModels: []string{"a", "b"},
		ModelAliases:   []freebuff.ModelAlias{{Name: "x", Alias: "y"}},
	})
	if !ad.Disabled {
		t.Fatal("Disabled")
	}
	if ad.Metadata["disabled"] != true {
		t.Fatalf("meta disabled %#v", ad.Metadata["disabled"])
	}
	if ad.Attributes["disabled"] != "true" {
		t.Fatal("attr disabled")
	}
	if ad.Attributes["auth_kind"] != "oauth" {
		t.Fatal("auth_kind")
	}
	if !strings.Contains(ad.Attributes["excluded_models"], "a") {
		t.Fatal("excluded attr")
	}
	if ad.Attributes["model_aliases"] == "" {
		t.Fatal("aliases attr")
	}
}

func TestPrepareRunRejectsDisabled(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	req := pluginapi.ExecutorRequest{
		StorageJSON: []byte(`{"token":"t","disabled":true}`),
		Model:       "deepseek/deepseek-v4-flash",
		Payload:     []byte(`{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}`),
	}
	raw, _ := json.Marshal(req)
	_, err := d.HandleExecExecute(raw)
	if err == nil || !strings.Contains(err.Error(), "auth_disabled") {
		t.Fatalf("err=%v", err)
	}
}

func TestPrepareRunRejectsExcludedModel(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	storage, _ := json.Marshal(map[string]any{
		"token":           "t",
		"excluded_models": []string{"deepseek/deepseek-v4-flash"},
	})
	req := pluginapi.ExecutorRequest{
		StorageJSON: storage,
		Model:       "deepseek/deepseek-v4-flash",
		Payload:     []byte(`{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}`),
	}
	raw, _ := json.Marshal(req)
	_, err := d.HandleExecExecute(raw)
	if err == nil || !strings.Contains(err.Error(), "model_excluded") {
		t.Fatalf("err=%v", err)
	}
}
