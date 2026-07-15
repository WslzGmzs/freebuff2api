package freebuff

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUpstreamErrorCarriesHTTPStatus(t *testing.T) {
	err := upstreamError(429, []byte(`{"model":"m","retryAfterMs":60000}`), nil, "Codebuff chat failed")
	fe, ok := err.(*Error)
	if !ok {
		t.Fatalf("type %T", err)
	}
	if fe.StatusCode() != 429 {
		t.Fatalf("status=%d", fe.StatusCode())
	}
	if fe.Code != "upstream_error" {
		t.Fatalf("code=%s", fe.Code)
	}
	if ra := fe.RetryAfter(); ra == nil || *ra < time.Second {
		t.Fatalf("retryAfter=%v", ra)
	}
}

func TestUpstreamErrorQuotaExhaustedDefaultCool(t *testing.T) {
	err := upstreamError(429, []byte(`额度已用尽`), nil, "Codebuff request failed")
	fe := err.(*Error)
	ra := fe.RetryAfter()
	if ra == nil || *ra < 10*time.Minute {
		t.Fatalf("expected long cool for quota body, got %v", ra)
	}
}

func TestUpstreamErrorRetryAfterHeader(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "120")
	err := upstreamError(429, []byte(`{}`), h, "x")
	fe := err.(*Error)
	ra := fe.RetryAfter()
	if ra == nil || *ra != 120*time.Second {
		t.Fatalf("got %v", ra)
	}
}

func TestRateLimitAsError(t *testing.T) {
	rl := RateLimit{
		Model:   "m",
		ResetAt: time.Now().UTC().Add(5 * time.Minute),
		RawBody: `{"model":"m"}`,
		Prefix:  "Codebuff chat failed",
	}
	fe := rl.AsError()
	if fe.StatusCode() != 429 {
		t.Fatal(fe.StatusCode())
	}
	if !strings.Contains(fe.Error(), "429") {
		t.Fatal(fe.Error())
	}
	if ra := fe.RetryAfter(); ra == nil {
		t.Fatal("nil retry")
	}
}

func TestIsQuotaExhaustedBody(t *testing.T) {
	if !isQuotaExhaustedBody([]byte(`{"error":{"data":{"code":14018}}}`)) {
		t.Fatal("14018")
	}
	if !isQuotaExhaustedBody([]byte(`rate limit exceeded`)) {
		t.Fatal("rate limit")
	}
	if isQuotaExhaustedBody([]byte(`ok`)) {
		t.Fatal("false positive")
	}
}
