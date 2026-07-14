package freebuff

import (
	"strings"
	"testing"
)

func TestLoginEndpoints(t *testing.T) {
	code, status := LoginEndpoints(LoginModeFreebuff)
	if code != "https://freebuff.com/api/auth/cli/code" {
		t.Fatalf("code=%s", code)
	}
	if status != "https://freebuff.com/api/auth/cli/status" {
		t.Fatalf("status=%s", status)
	}
	code, status = LoginEndpoints(LoginModeCodebuff)
	if code != "https://www.codebuff.com/api/auth/cli/code" {
		t.Fatalf("codebuff code=%s", code)
	}
	if status != "https://www.codebuff.com/api/auth/cli/status" {
		t.Fatalf("codebuff status=%s", status)
	}
}

func TestAuthStorageFromToken(t *testing.T) {
	sa := AuthStorageFromToken("tok", &LoginUser{Name: "Alice", Email: "a@b.c"}, LoginModeFreebuff)
	if sa.Token != "tok" {
		t.Fatal(sa.Token)
	}
	if !strings.Contains(sa.Label, "Alice") || !strings.Contains(sa.Label, "Freebuff OAuth") {
		t.Fatalf("label=%s", sa.Label)
	}
	if sa.LoginMode != string(LoginModeFreebuff) {
		t.Fatalf("login_mode=%s", sa.LoginMode)
	}
	if sa.Provider != "freebuff" {
		t.Fatalf("provider=%s", sa.Provider)
	}
	sa = AuthStorageFromToken("tok", nil, LoginModeCodebuff)
	if sa.Label != "Codebuff OAuth" {
		t.Fatalf("label=%s", sa.Label)
	}
}

func TestVerifyTokenEmpty(t *testing.T) {
	r := VerifyToken(t.Context(), "", "")
	if r.OK {
		t.Fatal("expected fail")
	}
}

func TestParseAuthStorageStandardFields(t *testing.T) {
	raw := []byte(`{
		"id": "freebuff-abc",
		"provider": "freebuff",
		"prefix": "fb",
		"label": "My FB",
		"disabled": false,
		"proxy_url": "socks5://127.0.0.1:1080",
		"priority": 10,
		"token": "tok-a,tok-b",
		"attributes": {"region": "cn"},
		"metadata": {"note": "x"}
	}`)
	sa, err := ParseAuthStorage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if sa.Prefix != "fb" || sa.ProxyURL == "" || sa.Priority != 10 {
		t.Fatalf("%+v", sa)
	}
	if len(sa.TokenList()) != 2 {
		t.Fatalf("tokens=%v", sa.TokenList())
	}
	sa.Normalize()
	if sa.Attributes["priority"] != "10" {
		t.Fatalf("attrs=%v", sa.Attributes)
	}
}

func TestMaybeGunzipBytes(t *testing.T) {
	// plain JSON unchanged
	in := []byte(`{"ok":true}`)
	if string(maybeGunzipBytes(in)) != string(in) {
		t.Fatal("plain json modified")
	}
}
