package freebuff

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Error is a Freebuff/Codebuff upstream failure.
//
// Implements CPA StatusCode() / RetryAfter() so the host MarkResult path can
// cool down and rotate credentials on 401/402/429 (same pattern as workbuddy).
type Error struct {
	Message    string
	HTTPStatus int
	Code       string // optional RPC error code (e.g. upstream_error)
	Retryable  bool
	retryAfter *time.Duration
}

func (e *Error) Error() string {
	if e == nil {
		return "freebuff error"
	}
	return e.Message
}

// StatusCode is the method CPA cliproxyexecutor.StatusError looks for.
func (e *Error) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.HTTPStatus
}

// RetryAfter is the method CPA retryAfterFromError looks for.
func (e *Error) RetryAfter() *time.Duration {
	if e == nil {
		return nil
	}
	return e.retryAfter
}

// WithRetryAfter sets a cooldown duration for host scheduling.
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	if e == nil {
		return nil
	}
	if d < 0 {
		d = 0
	}
	e.retryAfter = &d
	e.Retryable = true
	return e
}

// Session is an active Freebuff free session.
type Session struct {
	InstanceID  string
	Model       string
	ExpiresAt   string
	RemainingMs *int
}

// IsFresh reports whether the session is still worth reusing.
func (s Session) IsFresh() bool {
	if s.RemainingMs == nil {
		return true
	}
	return *s.RemainingMs > 60_000
}

// Run tracks the agent-run chain started for one chat completion.
type Run struct {
	RunID         string
	AgentID       string
	StartedAt     string
	ChildRunID    string
	ChatRunID     string
	ChatStartedAt string
}

// PayloadRunID is the run id embedded in codebuff_metadata.
func (r Run) PayloadRunID() string {
	if r.ChatRunID != "" {
		return r.ChatRunID
	}
	return r.RunID
}

// RateLimit caches an upstream 429 for fail-fast retries.
type RateLimit struct {
	Model        string
	ResetAt      time.Time
	RetryAfterMs int
	RawBody      string
	Prefix       string
}

func (r RateLimit) Active() bool {
	return time.Now().UTC().Before(r.ResetAt)
}

func (r RateLimit) FormatError() string {
	prefix := r.Prefix
	if prefix == "" {
		prefix = "Codebuff request failed"
	}
	return fmt.Sprintf("%s: 429 %s", prefix, r.RawBody)
}

// AsError returns a StatusError the CPA host can use for auth cooldown / rotation.
func (r RateLimit) AsError() *Error {
	err := &Error{
		Message:    r.FormatError(),
		HTTPStatus: http.StatusTooManyRequests,
		Code:       "upstream_error",
		Retryable:  true,
	}
	if !r.ResetAt.IsZero() {
		d := time.Until(r.ResetAt.UTC())
		if d < time.Second {
			d = time.Second
		}
		err.retryAfter = &d
	} else if r.RetryAfterMs > 0 {
		d := time.Duration(r.RetryAfterMs) * time.Millisecond
		err.retryAfter = &d
	} else {
		d := 30 * time.Minute
		err.retryAfter = &d
	}
	return err
}

// Client talks to Codebuff Freebuff HTTP APIs for one token.
type Client struct {
	settings Settings
	http     *http.Client

	mu              sync.Mutex
	agentsValidated bool
	rateLimitCache  map[string]RateLimit
}

// NewClient builds an HTTP client. Env proxies are never trusted (matches Python).
func NewClient(settings Settings) *Client {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if settings.ProxyURL != "" {
		if u, err := url.Parse(settings.ProxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	timeout := settings.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		settings: settings,
		http: &http.Client{
			Timeout:   0, // stream-friendly; per-request context enforces deadlines
			Transport: transport,
		},
		rateLimitCache: map[string]RateLimit{},
	}
}

// Settings returns a copy of client settings.
func (c *Client) Settings() Settings { return c.settings }

func (c *Client) headers(jsonBody bool, userAgent string, requireAuth bool, extra map[string]string) (http.Header, error) {
	if userAgent == "" {
		userAgent = JSONUserAgent
	}
	if requireAuth && strings.TrimSpace(c.settings.Token) == "" {
		return nil, &Error{Message: "FREEBUFF_TOKEN is required", HTTPStatus: 500}
	}
	h := make(http.Header)
	h.Set("Accept", "*/*")
	// Do NOT set Accept-Encoding. If the client requests gzip explicitly,
	// net/http will NOT auto-decompress and JSON parsing sees raw 0x1f gzip magic.
	// Letting Transport negotiate keeps transparent gzip decode.
	h.Set("Connection", "keep-alive")
	h.Set("Host", HostHeader(c.settings.BaseURL))
	h.Set("User-Agent", userAgent)
	if requireAuth {
		h.Set("Authorization", "Bearer "+c.settings.Token)
	}
	if jsonBody {
		h.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		h.Set(k, v)
	}
	return h, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, hdr http.Header) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	fullURL := c.settings.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return nil, err
	}
	for k, vals := range hdr {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	// Host header set via req.Host for http client.
	req.Host = HostHeader(c.settings.BaseURL)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Message: fmt.Sprintf("%s %s network error: %v", method, fullURL, err), HTTPStatus: 502}
	}
	defer resp.Body.Close()
	bodyReader := io.Reader(resp.Body)
	// Defensive: if Content-Encoding is still gzip (explicit Accept-Encoding elsewhere),
	// unwrap before JSON parse. Net/http auto-decompresses when it set Accept-Encoding itself.
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Encoding")), "gzip") {
		if gr, err := gzip.NewReader(resp.Body); err == nil {
			defer gr.Close()
			bodyReader = gr
		}
	}
	payload, _ := io.ReadAll(io.LimitReader(bodyReader, 4<<20))
	if resp.StatusCode >= 400 {
		c.maybeRecordRateLimit(resp.StatusCode, string(payload), "Codebuff request failed")
		return nil, upstreamError(resp.StatusCode, payload, resp.Header, "Codebuff request failed")
	}
	if len(payload) == 0 {
		return map[string]any{}, nil
	}
	payload = maybeGunzipBytes(payload)
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, &Error{Message: fmt.Sprintf("invalid JSON from %s: %v", path, err), HTTPStatus: 502}
	}
	return out, nil
}

func (c *Client) maybeRecordRateLimit(status int, text, prefix string) {
	if status != 429 || text == "" {
		return
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return
	}
	model, _ := data["model"].(string)
	if model == "" {
		return
	}
	retryAfterMs := 0
	switch v := data["retryAfterMs"].(type) {
	case float64:
		retryAfterMs = int(v)
	case int:
		retryAfterMs = v
	}
	resetAt := parseRateLimitReset(data["resetAt"])
	if resetAt.IsZero() && retryAfterMs > 0 {
		resetAt = time.Now().UTC().Add(time.Duration(retryAfterMs) * time.Millisecond)
	}
	if resetAt.IsZero() {
		return
	}
	raw := text
	if len(raw) > 500 {
		raw = raw[:500]
	}
	c.mu.Lock()
	c.rateLimitCache[model] = RateLimit{
		Model: model, ResetAt: resetAt, RetryAfterMs: retryAfterMs, RawBody: raw, Prefix: prefix,
	}
	c.mu.Unlock()
}

// CachedRateLimit returns an active cached 429 for the model if present.
func (c *Client) CachedRateLimit(model string) (RateLimit, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rl, ok := c.rateLimitCache[model]
	if !ok || !rl.Active() {
		return RateLimit{}, false
	}
	return rl, true
}

// ValidateAgents posts agent definitions once per client lifetime.
func (c *Client) ValidateAgents(ctx context.Context, active []Model) error {
	c.mu.Lock()
	if c.agentsValidated {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	hdr, err := c.headers(true, JSONUserAgent, false, nil)
	if err != nil {
		return err
	}
	_, err = c.doJSON(ctx, http.MethodPost, "/api/agents/validate", AgentValidationPayload(active), hdr)
	c.mu.Lock()
	c.agentsValidated = true
	c.mu.Unlock()
	// Soft-fail: Python continues even when validation errors.
	_ = err
	return nil
}

// GetSession fetches the freebuff session status.
func (c *Client) GetSession(ctx context.Context, instanceID string) (map[string]any, error) {
	extra := map[string]string{}
	if instanceID != "" {
		extra["x-freebuff-instance-id"] = instanceID
	}
	hdr, err := c.headers(false, JSONUserAgent, true, extra)
	if err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodGet, "/api/v1/freebuff/session", nil, hdr)
}

// CreateSession creates (or waits for) an active free session for model.
func (c *Client) CreateSession(ctx context.Context, model string) (Session, error) {
	hdr, err := c.headers(false, JSONUserAgent, true, map[string]string{"x-freebuff-model": model})
	if err != nil {
		return Session{}, err
	}
	data, err := c.doJSON(ctx, http.MethodPost, "/api/v1/freebuff/session", nil, hdr)
	if err != nil {
		return Session{}, err
	}
	if status, _ := data["status"].(string); status == "queued" {
		return c.waitForActiveSession(ctx, data, model)
	}
	return sessionFromData(data, model, "")
}

func sessionFromData(data map[string]any, model, instanceID string) (Session, error) {
	resolved := stringField(data, "instanceId")
	if resolved == "" {
		resolved = instanceID
	}
	if stringField(data, "status") != "active" || resolved == "" {
		return Session{}, &Error{Message: fmt.Sprintf("Freebuff session is not active: %v", data), HTTPStatus: 502}
	}
	s := Session{
		InstanceID: resolved,
		Model:      firstNonEmpty(stringField(data, "model"), model),
		ExpiresAt:  stringField(data, "expiresAt"),
	}
	if v, ok := data["remainingMs"].(float64); ok {
		ms := int(v)
		s.RemainingMs = &ms
	}
	return s, nil
}

func (c *Client) waitForActiveSession(ctx context.Context, data map[string]any, model string) (Session, error) {
	instanceID := stringField(data, "instanceId")
	if instanceID == "" {
		return Session{}, &Error{Message: fmt.Sprintf("Freebuff queued session id missing: %v", data), HTTPStatus: 502}
	}
	deadline := time.Now().Add(c.settings.Timeout)
	if c.settings.Timeout <= 0 {
		deadline = time.Now().Add(DefaultTimeout)
	}
	attempts := 0
	for stringField(data, "status") == "queued" {
		if time.Now().After(deadline) {
			return Session{}, &Error{Message: fmt.Sprintf("Freebuff session did not become active before timeout: %v", data), HTTPStatus: 502}
		}
		if attempts > 0 {
			select {
			case <-ctx.Done():
				return Session{}, ctx.Err()
			case <-time.After(queuePollDelay(data["estimatedWaitMs"])):
			}
		}
		var err error
		data, err = c.GetSession(ctx, instanceID)
		if err != nil {
			return Session{}, err
		}
		attempts++
	}
	return sessionFromData(data, model, instanceID)
}

// DeleteSession deletes the active freebuff session for this token.
func (c *Client) DeleteSession(ctx context.Context) error {
	hdr, err := c.headers(false, JSONUserAgent, true, nil)
	if err != nil {
		return err
	}
	_, err = c.doJSON(ctx, http.MethodDelete, "/api/v1/freebuff/session", nil, hdr)
	return err
}

// GetStreak fetches freebuff streak info.
func (c *Client) GetStreak(ctx context.Context) (map[string]any, error) {
	hdr, err := c.headers(false, JSONUserAgent, true, nil)
	if err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodGet, "/api/v1/freebuff/streak", nil, hdr)
}

// RequestAds requests ads for a provider.
func (c *Client) RequestAds(ctx context.Context, provider string, messages []map[string]any, surface string) (map[string]any, error) {
	body := map[string]any{
		"provider":  provider,
		"messages":  adMessages(messages),
		"sessionId": c.settings.SessionID,
		"device": map[string]any{
			"os":       c.settings.OSName,
			"timezone": c.settings.Timezone,
			"locale":   c.settings.Locale,
		},
		"userAgent": BrowserUserAgent,
	}
	if surface != "" {
		body["surface"] = surface
	}
	hdr, err := c.headers(true, CLIUserAgent, true, nil)
	if err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/ads", body, hdr)
}

// RequestAdChain tries ad providers in order until one yields an impression.
func (c *Client) RequestAdChain(ctx context.Context, messages []map[string]any, surface string, withStreak bool) {
	for _, provider := range c.settings.AdProviders {
		adsData, err := c.RequestAds(ctx, provider, messages, surface)
		if err != nil {
			continue
		}
		ads, _ := adsData["ads"].([]any)
		if len(ads) == 0 {
			continue
		}
		ad, _ := ads[0].(map[string]any)
		if ad == nil {
			continue
		}
		if withStreak {
			_, _ = c.GetStreak(ctx)
		}
		var ids []string
		if raw, ok := ad["impressionIds"].([]any); ok {
			for _, id := range raw {
				if s, ok := id.(string); ok {
					ids = append(ids, s)
				}
			}
		}
		_ = c.ReportZeroClickImpressions(ctx, ids)
		_ = c.ReportCodebuffImpression(ctx, stringField(ad, "impUrl"))
		return
	}
}

// ReportZeroClickImpressions posts impression ids to zeroclick.dev.
func (c *Client) ReportZeroClickImpressions(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"ids": ids})
	fullURL := c.settings.ZeroClickURL + "/api/v2/impressions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", JSONUserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Message: fmt.Sprintf("zeroclick network error: %v", err), HTTPStatus: 502}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return &Error{Message: fmt.Sprintf("Zeroclick impression failed: %d %s", resp.StatusCode, truncate(string(body), 500)), HTTPStatus: 502}
	}
	return nil
}

// ReportCodebuffImpression reports an ad impression URL.
func (c *Client) ReportCodebuffImpression(ctx context.Context, impURL string) error {
	if impURL == "" {
		return nil
	}
	hdr, err := c.headers(true, CLIUserAgent, true, nil)
	if err != nil {
		return err
	}
	_, err = c.doJSON(ctx, http.MethodPost, "/api/v1/ads/impression", map[string]any{
		"impUrl": impURL,
		"mode":   "LITE",
	}, hdr)
	return err
}

// StartRun starts an agent run and returns runId.
func (c *Client) StartRun(ctx context.Context, agentID string, ancestorRunIDs []string) (string, error) {
	if ancestorRunIDs == nil {
		ancestorRunIDs = []string{}
	}
	hdr, err := c.headers(true, JSONUserAgent, true, nil)
	if err != nil {
		return "", err
	}
	data, err := c.doJSON(ctx, http.MethodPost, "/api/v1/agent-runs", map[string]any{
		"action":         "START",
		"agentId":        agentID,
		"ancestorRunIds": ancestorRunIDs,
	}, hdr)
	if err != nil {
		return "", err
	}
	runID := stringField(data, "runId")
	if runID == "" {
		return "", &Error{Message: fmt.Sprintf("Codebuff run id missing: %v", data), HTTPStatus: 502}
	}
	return runID, nil
}

// RecordRunStep records a completed run step.
func (c *Client) RecordRunStep(ctx context.Context, runID string, stepNumber int, messageID *string, startTime string, childRunIDs []string) error {
	if childRunIDs == nil {
		childRunIDs = []string{}
	}
	body := map[string]any{
		"stepNumber":  stepNumber,
		"credits":     0,
		"childRunIds": childRunIDs,
		"status":      "completed",
		"startTime":   startTime,
	}
	if messageID != nil {
		body["messageId"] = *messageID
	} else {
		body["messageId"] = nil
	}
	hdr, err := c.headers(true, JSONUserAgent, true, nil)
	if err != nil {
		return err
	}
	_, err = c.doJSON(ctx, http.MethodPost, "/api/v1/agent-runs/"+runID+"/steps", body, hdr)
	return err
}

// FinishRun finishes an agent run.
func (c *Client) FinishRun(ctx context.Context, runID string, totalSteps int) error {
	hdr, err := c.headers(true, JSONUserAgent, true, nil)
	if err != nil {
		return err
	}
	_, err = c.doJSON(ctx, http.MethodPost, "/api/v1/agent-runs", map[string]any{
		"action":        "FINISH",
		"runId":         runID,
		"status":        "completed",
		"totalSteps":    totalSteps,
		"directCredits": 0,
		"totalCredits":  0,
	}, hdr)
	return err
}

// ChatEvent is either a parsed JSON object or the string "[DONE]".
type ChatEvent struct {
	Object map[string]any
	Done   bool
	Raw    string
}

// ChatEvents streams POST /api/v1/chat/completions SSE events.
func (c *Client) ChatEvents(ctx context.Context, payload map[string]any, handle func(ChatEvent) error) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	fullURL := c.settings.BaseURL + "/api/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	hdr, err := c.headers(true, ChatUserAgent, true, nil)
	if err != nil {
		return err
	}
	for k, vals := range hdr {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	req.Host = HostHeader(c.settings.BaseURL)

	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Message: fmt.Sprintf("POST %s network error: %v", fullURL, err), HTTPStatus: 502}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		c.maybeRecordRateLimit(resp.StatusCode, string(body), "Codebuff chat failed")
		return upstreamError(resp.StatusCode, body, resp.Header, "Codebuff chat failed")
	}
	return scanSSE(resp.Body, handle)
}

// ChatStream is an open upstream chat SSE response (status already 2xx).
type ChatStream struct {
	Body   io.ReadCloser
	Header http.Header
}

// OpenChatStream POSTs chat completions and returns the body only after a
// successful status. 4xx/429 are returned synchronously so CPA execute_stream
// can cool down / rotate credentials (async mid-stream errors cannot).
func (c *Client) OpenChatStream(ctx context.Context, payload map[string]any) (*ChatStream, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	fullURL := c.settings.BaseURL + "/api/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	hdr, err := c.headers(true, ChatUserAgent, true, nil)
	if err != nil {
		return nil, err
	}
	for k, vals := range hdr {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	req.Host = HostHeader(c.settings.BaseURL)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Message: fmt.Sprintf("POST %s network error: %v", fullURL, err), HTTPStatus: 502, Code: "upstream_error", Retryable: true}
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		c.maybeRecordRateLimit(resp.StatusCode, string(body), "Codebuff chat failed")
		return nil, upstreamError(resp.StatusCode, body, resp.Header, "Codebuff chat failed")
	}
	return &ChatStream{Body: resp.Body, Header: resp.Header}, nil
}

// PumpChatStream reads SSE frames from an open chat stream.
func PumpChatStream(body io.Reader, handle func(ChatEvent) error) error {
	return scanSSE(body, handle)
}

// FetchAvailableModels discovers models from rateLimitsByModel.
func (c *Client) FetchAvailableModels(ctx context.Context) ([]Model, error) {
	hdr, err := c.headers(false, JSONUserAgent, true, nil)
	if err != nil {
		return nil, err
	}
	data, err := c.doJSON(ctx, http.MethodGet, "/api/v1/freebuff/session", nil, hdr)
	if err != nil {
		return nil, err
	}
	rateLimits, _ := data["rateLimitsByModel"].(map[string]any)
	var models []Model
	for modelID, info := range rateLimits {
		if modelID == "" {
			continue
		}
		ownedBy := "freebuff"
		if m, ok := info.(map[string]any); ok {
			if v, ok := m["owned_by"].(string); ok && v != "" {
				ownedBy = v
			} else if v, ok := m["ownedBy"].(string); ok && v != "" {
				ownedBy = v
			}
		}
		models = append(models, Model{
			ID:          modelID,
			AgentID:     MapModelToAgentID(modelID),
			OwnedBy:     ownedBy,
			DisplayName: DeriveDisplayName(modelID),
		})
	}
	return models, nil
}

func scanSSE(r io.Reader, handle func(ChatEvent) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var dataLines []string
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if data == "[DONE]" {
			return handle(ChatEvent{Done: true, Raw: data})
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(data), &obj); err != nil {
			return nil // ignore non-json frames
		}
		return handle(ChatEvent{Object: obj, Raw: data})
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return scanner.Err()
}

func upstreamError(status int, body []byte, headers http.Header, prefix string) error {
	text := strings.TrimSpace(string(body))
	if text == "" {
		text = http.StatusText(status)
	}
	err := &Error{
		Message:    fmt.Sprintf("%s: %d %s", prefix, status, truncate(text, 500)),
		HTTPStatus: status,
		Code:       "upstream_error",
		Retryable:  status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500,
	}
	if status == http.StatusTooManyRequests {
		if ra := parseRetryAfterHeader(headers); ra != nil {
			err.retryAfter = ra
		} else if d := retryAfterFromRateLimitBody(body); d != nil {
			err.retryAfter = d
		} else if isQuotaExhaustedBody(body) {
			// Permanent-ish free-tier / quota window; cool long enough to stop hammering.
			cool := 30 * time.Minute
			err.retryAfter = &cool
		}
	}
	// 401/403: short cool-down so multi-token pools can rotate.
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		cool := 5 * time.Minute
		err.retryAfter = &cool
	}
	return err
}

func parseRetryAfterHeader(headers http.Header) *time.Duration {
	if headers == nil {
		return nil
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return nil
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		d := time.Duration(secs) * time.Second
		return &d
	}
	if t, err := http.ParseTime(raw); err == nil {
		d := time.Until(t)
		if d > 0 {
			return &d
		}
	}
	return nil
}

// retryAfterFromRateLimitBody parses Freebuff/Codebuff 429 JSON:
//
//	{"model":"...","resetAt":"...","retryAfterMs":...}
func retryAfterFromRateLimitBody(body []byte) *time.Duration {
	if len(body) == 0 {
		return nil
	}
	var data map[string]any
	if json.Unmarshal(body, &data) != nil {
		return nil
	}
	if resetAt := parseRateLimitReset(data["resetAt"]); !resetAt.IsZero() {
		d := time.Until(resetAt)
		if d < time.Second {
			d = time.Second
		}
		return &d
	}
	switch v := data["retryAfterMs"].(type) {
	case float64:
		if v > 0 {
			d := time.Duration(v) * time.Millisecond
			return &d
		}
	case int:
		if v > 0 {
			d := time.Duration(v) * time.Millisecond
			return &d
		}
	}
	return nil
}

func isQuotaExhaustedBody(body []byte) bool {
	s := string(body)
	if s == "" {
		return false
	}
	// Freebuff/Codebuff free-tier and CodeBuddy-style messages.
	if strings.Contains(s, "14018") || strings.Contains(s, "额度已用尽") {
		return true
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "ratelimit") {
		return true
	}
	return strings.Contains(lower, "quota") &&
		(strings.Contains(lower, "exhaust") || strings.Contains(lower, "exceed") || strings.Contains(lower, "insufficient") || strings.Contains(lower, "limit"))
}

func parseRateLimitReset(v any) time.Time {
	s, _ := v.(string)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func queuePollDelay(estimated any) time.Duration {
	ms := 0.0
	switch v := estimated.(type) {
	case float64:
		ms = v
	case int:
		ms = float64(v)
	}
	if ms <= 0 {
		return 1500 * time.Millisecond
	}
	d := time.Duration(ms/4) * time.Millisecond
	if d < 500*time.Millisecond {
		d = 500 * time.Millisecond
	}
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

func adMessages(messages []map[string]any) []map[string]string {
	out := make([]map[string]string, 0, len(messages))
	for _, m := range messages {
		role := adMessageRole(m["role"])
		content := adMessageContent(m["content"])
		if content == "" {
			continue
		}
		out = append(out, map[string]string{"role": role, "content": content})
	}
	return out
}

func adMessageRole(v any) string {
	s, _ := v.(string)
	if s == "" {
		return "user"
	}
	return s
}

func adMessageContent(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var parts []string
		for _, p := range t {
			if m, ok := p.(map[string]any); ok {
				if m["type"] == "text" {
					if s, ok := m["text"].(string); ok {
						parts = append(parts, s)
					}
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// UTCNowISO returns an RFC3339 milli timestamp with Z suffix.
func UTCNowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// maybeGunzipBytes decompresses if payload starts with gzip magic 0x1f 0x8b.
func maybeGunzipBytes(payload []byte) []byte {
	if len(payload) < 2 || payload[0] != 0x1f || payload[1] != 0x8b {
		return payload
	}
	gr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return payload
	}
	defer gr.Close()
	out, err := io.ReadAll(io.LimitReader(gr, 4<<20))
	if err != nil || len(out) == 0 {
		return payload
	}
	return out
}
