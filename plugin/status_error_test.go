package plugin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WslzGmzs/freebuff2api/freebuff"
)

func TestErrorEnvelopeFromErrHTTPStatus(t *testing.T) {
	err := &freebuff.Error{
		Message:    "Codebuff chat failed: 429 quota",
		HTTPStatus: 429,
		Code:       "upstream_error",
		Retryable:  true,
	}
	err = err.WithRetryAfter(30 * time.Minute)
	raw := ErrorEnvelopeFromErr(err)
	var env Envelope
	if json.Unmarshal(raw, &env) != nil || env.OK || env.Error == nil {
		t.Fatalf("%s", raw)
	}
	if env.Error.HTTPStatus != 429 {
		t.Fatalf("http_status=%d", env.Error.HTTPStatus)
	}
	if env.Error.Code != "upstream_error" {
		t.Fatalf("code=%s", env.Error.Code)
	}
	if !env.Error.Retryable {
		t.Fatal("retryable")
	}
}

func TestErrorEnvelopeFromAuthDisabled(t *testing.T) {
	raw := ErrorEnvelopeFromErr(&freebuff.Error{
		Message:    "auth_disabled: freebuff credential is disabled",
		HTTPStatus: 403,
		Code:       "auth_disabled",
	})
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	if env.Error.HTTPStatus != 403 || env.Error.Code != "auth_disabled" {
		t.Fatalf("%+v", env.Error)
	}
}

func TestErrorEnvelopeFromString429(t *testing.T) {
	raw := ErrorEnvelopeFromErr(errString("something 429 rate limit"))
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	if env.Error.HTTPStatus != 429 {
		t.Fatalf("%+v", env.Error)
	}
	if !strings.Contains(env.Error.Message, "429") {
		t.Fatal(env.Error.Message)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
