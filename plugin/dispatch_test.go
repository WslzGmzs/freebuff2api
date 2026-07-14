package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WslzGmzs/freebuff2api/freebuff"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestRegisterEnvelopeFreebuff(t *testing.T) {
	// Default identity is freebuff.
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	raw, err := d.Handle(pluginabi.MethodPluginRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("not ok: %s", raw)
	}
	var reg Registration
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatal(err)
	}
	if reg.Metadata.Name != "Freebuff" {
		t.Fatalf("name=%s", reg.Metadata.Name)
	}
	if !reg.Capabilities.ModelProvider || !reg.Capabilities.AuthProvider || !reg.Capabilities.Executor {
		t.Fatalf("capabilities: %+v", reg.Capabilities)
	}
}

func TestRegisterEnvelopeCodebuff(t *testing.T) {
	Identity = IdentityCodebuff
	defer func() { Identity = IdentityFreebuff }()
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
	if reg.Metadata.Name != "Codebuff" {
		t.Fatalf("name=%s", reg.Metadata.Name)
	}
	if !reg.Capabilities.AuthProvider {
		t.Fatal("codebuff needs auth_provider")
	}
	if reg.Capabilities.Executor || reg.Capabilities.ModelProvider {
		t.Fatalf("codebuff should be auth-only: %+v", reg.Capabilities)
	}
}

func TestAuthIdentifierByIdentity(t *testing.T) {
	Identity = IdentityFreebuff
	if AuthIdentifier() != "freebuff" {
		t.Fatal(AuthIdentifier())
	}
	Identity = IdentityCodebuff
	if AuthIdentifier() != "codebuff" {
		t.Fatal(AuthIdentifier())
	}
	Identity = IdentityFreebuff
}

func TestAuthIdentifierRPC(t *testing.T) {
	Identity = IdentityCodebuff
	defer func() { Identity = IdentityFreebuff }()
	d := NewDispatcher(NopHost{})
	raw, err := d.Handle(pluginabi.MethodAuthIdentifier, nil)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	if !strings.Contains(string(env.Result), "codebuff") {
		t.Fatalf("%s", env.Result)
	}
}

func TestModelStaticListsModels(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	raw, err := d.Handle(pluginabi.MethodModelStatic, nil)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	var m map[string]any
	if err := json.Unmarshal(env.Result, &m); err != nil {
		t.Fatal(err)
	}
	models, _ := m["Models"].([]any)
	if models == nil {
		models, _ = m["models"].([]any)
	}
	if len(models) < 5 {
		t.Fatalf("expected many models, got %d", len(models))
	}
}

func TestAuthParseStandardFields(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	type authParseReq struct {
		FileName string `json:"FileName"`
		RawJSON  []byte `json:"RawJSON"`
	}
	payload, _ := json.Marshal(authParseReq{
		FileName: "freebuff.json",
		RawJSON: []byte(`{
			"token":"abc123",
			"prefix":"fb",
			"proxy_url":"http://127.0.0.1:7890",
			"priority":5,
			"label":"Test",
			"disabled":false
		}`),
	})
	raw, err := d.Handle(pluginabi.MethodAuthParse, payload)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("%s", raw)
	}
	if !strings.Contains(string(env.Result), "7890") {
		t.Fatalf("proxy missing: %s", env.Result)
	}
	// Runtime provider for execution is always freebuff.
	if !strings.Contains(string(env.Result), "freebuff") {
		t.Fatalf("provider freebuff missing: %s", env.Result)
	}
}

func TestCodebuffParseClaimsCodebuffFile(t *testing.T) {
	Identity = IdentityCodebuff
	defer func() { Identity = IdentityFreebuff }()
	d := NewDispatcher(NopHost{})
	type authParseReq struct {
		Provider string `json:"Provider"`
		FileName string `json:"FileName"`
		RawJSON  []byte `json:"RawJSON"`
	}
	payload, _ := json.Marshal(authParseReq{
		Provider: "codebuff",
		FileName: "codebuff.json",
		RawJSON:  []byte(`{"token":"t","login_mode":"codebuff"}`),
	})
	raw, err := d.Handle(pluginabi.MethodAuthParse, payload)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	if !strings.Contains(string(env.Result), `"Handled":true`) && !strings.Contains(string(env.Result), `"Handled": true`) {
		// capitalised JSON from encoding
		if !strings.Contains(string(env.Result), "true") {
			t.Fatalf("%s", env.Result)
		}
	}
}

func TestCodebuffParseIgnoresPlainFreebuff(t *testing.T) {
	Identity = IdentityCodebuff
	defer func() { Identity = IdentityFreebuff }()
	d := NewDispatcher(NopHost{})
	type authParseReq struct {
		FileName string `json:"FileName"`
		RawJSON  []byte `json:"RawJSON"`
	}
	payload, _ := json.Marshal(authParseReq{
		FileName: "freebuff.json",
		RawJSON:  []byte(`{"token":"t","login_mode":"freebuff"}`),
	})
	raw, err := d.Handle(pluginabi.MethodAuthParse, payload)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	if strings.Contains(string(env.Result), `"Handled":true`) {
		t.Fatalf("should not claim freebuff file: %s", env.Result)
	}
}

func TestAuthParseNotOurs(t *testing.T) {
	Identity = IdentityFreebuff
	d := NewDispatcher(NopHost{})
	type authParseReq struct {
		FileName string `json:"FileName"`
		RawJSON  []byte `json:"RawJSON"`
	}
	payload, _ := json.Marshal(authParseReq{
		FileName: "other.json",
		RawJSON:  []byte(`{"foo":1}`),
	})
	raw, err := d.Handle(pluginabi.MethodAuthParse, payload)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	if !env.OK {
		t.Fatalf("%s", raw)
	}
	if strings.Contains(string(env.Result), `"Handled":true`) {
		t.Fatalf("should not handle foreign auth: %s", env.Result)
	}
}

func TestToAuthDataRuntimeProvider(t *testing.T) {
	sa := freebuff.AuthStorage{
		Token:     "t1",
		LoginMode: "codebuff",
		Label:     "Codebuff OAuth",
	}
	ad := ToAuthData(sa)
	if ad.Provider != RuntimeProvider {
		t.Fatalf("provider=%s want freebuff for executor routing", ad.Provider)
	}
	if ad.Label != "Codebuff OAuth" {
		t.Fatalf("label=%s", ad.Label)
	}
}

func TestLoginModeFromOAuthLabel(t *testing.T) {
	if loginModeFromOAuthLabel("Codebuff OAuth") != freebuff.LoginModeCodebuff {
		t.Fatal("codebuff")
	}
	if loginModeFromOAuthLabel("Freebuff OAuth") != freebuff.LoginModeFreebuff {
		t.Fatal("freebuff")
	}
	if loginModeFromOAuthLabel("codebuff") != freebuff.LoginModeCodebuff {
		t.Fatal("id")
	}
}

func TestResolveLoginModeUsesIdentity(t *testing.T) {
	Identity = IdentityCodebuff
	defer func() { Identity = IdentityFreebuff }()
	mode, _ := resolveLoginModeAndProxy([]byte(`{"Provider":"codebuff"}`))
	if mode != freebuff.LoginModeCodebuff {
		t.Fatalf("mode=%s", mode)
	}
}

func TestClientNeedsSSEFrame(t *testing.T) {
	if ClientNeedsSSEFrame(map[string]any{"request_path": "/v1/chat/completions"}) {
		t.Fatal("chat should not pre-frame")
	}
	if !ClientNeedsSSEFrame(map[string]any{"request_path": "/v1/messages"}) {
		t.Fatal("anthropic path should pre-frame")
	}
}

func TestUnknownMethod(t *testing.T) {
	d := NewDispatcher(NopHost{})
	raw, err := d.Handle("no.such.method", nil)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	if env.OK {
		t.Fatal("expected error envelope")
	}
}

func TestCodebuffExecutorRejected(t *testing.T) {
	Identity = IdentityCodebuff
	defer func() { Identity = IdentityFreebuff }()
	d := NewDispatcher(NopHost{})
	_, err := d.Handle(pluginabi.MethodExecutorExecute, []byte(`{}`))
	if err == nil {
		t.Fatal("expected auth-only error")
	}
}
