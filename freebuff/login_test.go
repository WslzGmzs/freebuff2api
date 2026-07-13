package freebuff

import "testing"

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
	if sa.Label != "Alice" {
		t.Fatalf("label=%s", sa.Label)
	}
	sa = AuthStorageFromToken("tok", nil, LoginModeCodebuff)
	if sa.Label != "Freebuff (codebuff)" {
		t.Fatalf("label=%s", sa.Label)
	}
}

func TestVerifyTokenEmpty(t *testing.T) {
	r := VerifyToken(t.Context(), "", "")
	if r.OK {
		t.Fatal("expected fail")
	}
}
