package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/WslzGmzs/freebuff2api/freebuff"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestRegisterEnvelope(t *testing.T) {
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
	if len(reg.Capabilities.ExecutorInputFormats) == 0 {
		t.Fatal("missing executor formats")
	}
}

func TestModelStaticListsModels(t *testing.T) {
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
		t.Fatalf("expected many models, got %d: %s", len(models), string(env.Result)[:min(400, len(env.Result))])
	}
}

func TestAuthParseStandardFields(t *testing.T) {
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
	if !strings.Contains(string(env.Result), `"Prefix":"fb"`) && !strings.Contains(string(env.Result), `"prefix":"fb"`) {
		// exported field names on AuthData are capitalised without json tags typically
		if !strings.Contains(string(env.Result), "fb") {
			t.Fatalf("prefix missing: %s", env.Result)
		}
	}
	if !strings.Contains(string(env.Result), "7890") {
		t.Fatalf("proxy missing: %s", env.Result)
	}
}

func TestAuthParseNotOurs(t *testing.T) {
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

func TestToAuthDataMapsHostFields(t *testing.T) {
	sa := freebuff.AuthStorage{
		Token:    "t1",
		Prefix:   "my",
		ProxyURL: "socks5://1.2.3.4:1080",
		Priority: 7,
		Label:    "L",
	}
	ad := ToAuthData(sa)
	if ad.Prefix != "my" || ad.ProxyURL == "" || ad.Provider != ProviderName {
		t.Fatalf("%+v", ad)
	}
	if ad.ID == "" {
		t.Fatal("id empty")
	}
	if ad.Attributes["priority"] != "7" {
		t.Fatalf("attrs=%v", ad.Attributes)
	}
}

func TestLoginModeFromOAuthLabel(t *testing.T) {
	if loginModeFromOAuthLabel("Codebuff OAuth") != freebuff.LoginModeCodebuff {
		t.Fatal("codebuff")
	}
	if loginModeFromOAuthLabel("Freebuff OAuth") != freebuff.LoginModeFreebuff {
		t.Fatal("freebuff")
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

func TestManagementMethodsRemoved(t *testing.T) {
	d := NewDispatcher(NopHost{})
	raw, err := d.Handle(pluginabi.MethodManagementRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	if env.OK {
		t.Fatal("management should be unknown_method")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
