// Package plugin contains pure-Go CPA method handlers shared with the c-shared main package.
// The C ABI entry points live in package main; this package is unit-testable without CGO.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/WslzGmzs/freebuff2api/freebuff"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	ProviderName = "freebuff"
	AuthFileName = "freebuff.json"
)

// PluginVer is the plugin release version (no leading "v").
// Overridden at link time: -X github.com/WslzGmzs/freebuff2api/plugin.PluginVer=x.y.z
var PluginVer = "0.1.0"

// Host is the subset of host callbacks the executor needs.
type Host interface {
	StreamEmit(streamID string, payload []byte) error
	StreamEmitError(streamID, message string)
	StreamClose(streamID string)
	Log(level, message string)
}

// NopHost is a no-op Host for tests and non-stream paths.
type NopHost struct{}

func (NopHost) StreamEmit(string, []byte) error { return nil }
func (NopHost) StreamEmitError(string, string)  {}
func (NopHost) StreamClose(string)              {}
func (NopHost) Log(string, string)              {}

// Dispatcher routes CPA plugin methods.
type Dispatcher struct {
	Host Host

	poolCache   map[string]*freebuff.AccountPool
	poolCacheMu sync.Mutex

	dynamicModels   []freebuff.Model
	dynamicModelsMu sync.RWMutex

	// loginSessions holds in-flight CLI device-code logins (tool/get_token flow).
	loginSessions sync.Map // state(string) -> *freebuff.LoginSession
}

// NewDispatcher creates a dispatcher with optional host callbacks.
func NewDispatcher(host Host) *Dispatcher {
	if host == nil {
		host = NopHost{}
	}
	return &Dispatcher{
		Host:      host,
		poolCache: map[string]*freebuff.AccountPool{},
	}
}

// Handle dispatches one CPA RPC method and returns a JSON envelope.
func (d *Dispatcher) Handle(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return OkEnvelope(d.Registration())
	case pluginabi.MethodModelStatic, pluginabi.MethodModelForAuth:
		return OkEnvelope(pluginapi.ModelResponse{Provider: ProviderName, Models: d.Models()})
	case pluginabi.MethodModelRegister:
		return OkEnvelope(pluginapi.ModelRegistrationResponse{Provider: ProviderName, Models: d.Models()})
	case pluginabi.MethodAuthIdentifier:
		return OkEnvelope(IdentifierResponse{Identifier: ProviderName})
	case pluginabi.MethodAuthParse:
		return d.HandleParseAuth(request)
	case pluginabi.MethodAuthLoginStart:
		return d.HandleStartLogin(request)
	case pluginabi.MethodAuthLoginPoll:
		return d.HandlePollLogin(request)
	case pluginabi.MethodAuthRefresh:
		return d.HandleRefreshAuth(request)
	case pluginabi.MethodExecutorIdentifier:
		return OkEnvelope(IdentifierResponse{Identifier: ProviderName})
	case pluginabi.MethodExecutorExecute:
		return d.HandleExecExecute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return d.HandleExecStream(request)
	case pluginabi.MethodManagementRegister:
		return OkEnvelope(managementRegistration())
	case pluginabi.MethodManagementHandle:
		return d.HandleManagement(request)
	default:
		return ErrorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

// ----- registration / models -----

type Envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *EnvelopeError  `json:"error,omitempty"`
}

type EnvelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type IdentifierResponse struct {
	Identifier string `json:"identifier"`
}

type Registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  RegistrationCapability `json:"capabilities"`
}

type RegistrationCapability struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
	ManagementAPI         bool                         `json:"management_api"`
}

type StreamResponse struct {
	Headers http.Header                     `json:"headers,omitempty"`
	Chunks  []pluginapi.ExecutorStreamChunk `json:"chunks,omitempty"`
}

type ExecutorStreamRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func (d *Dispatcher) Registration() Registration {
	return Registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             ProviderName,
			Version:          PluginVer,
			Author:           "WslzGmzs",
			GitHubRepository: "https://github.com/WslzGmzs/freebuff2api",
			ConfigFields: []pluginapi.ConfigField{
				{
					Name:        "debug",
					Type:        pluginapi.ConfigFieldTypeBoolean,
					Description: "Enable verbose Freebuff upstream logging (also settable per-credential via freebuff.json debug).",
				},
				{
					Name:        "login_mode",
					Type:        pluginapi.ConfigFieldTypeEnum,
					EnumValues:  []string{"freebuff", "codebuff"},
					Description: "Default CLI login host for auth.login.start (freebuff.com or codebuff.com).",
				},
			},
		},
		Capabilities: RegistrationCapability{
			ModelProvider:         true,
			AuthProvider:          true,
			Executor:              true,
			ExecutorModelScope:    pluginapi.ExecutorModelScopeBoth,
			ExecutorInputFormats:  []string{"chat-completions"},
			ExecutorOutputFormats: []string{"chat-completions"},
			ManagementAPI:         true,
		},
	}
}

func (d *Dispatcher) ActiveModels() []freebuff.Model {
	d.dynamicModelsMu.RLock()
	dyn := d.dynamicModels
	d.dynamicModelsMu.RUnlock()
	if len(dyn) == 0 {
		return freebuff.AllModels
	}
	byID := map[string]freebuff.Model{}
	out := make([]freebuff.Model, 0, len(dyn)+len(freebuff.AllModels))
	for _, m := range dyn {
		byID[m.ID] = m
		out = append(out, m)
	}
	for _, m := range freebuff.AllModels {
		if _, ok := byID[m.ID]; !ok {
			out = append(out, m)
		}
	}
	return out
}

func (d *Dispatcher) Models() []pluginapi.ModelInfo {
	const maxCompletionTokens int64 = 8192
	const contextLength int64 = 200000
	models := d.ActiveModels()
	out := make([]pluginapi.ModelInfo, 0, len(models)*2)
	seen := map[string]struct{}{}
	add := func(id, owned, display string) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, pluginapi.ModelInfo{
			ID:                         id,
			Object:                     "model",
			OwnedBy:                    owned,
			DisplayName:                display,
			Name:                       id,
			SupportedGenerationMethods: []string{"chat"},
			ContextLength:              contextLength,
			MaxCompletionTokens:        maxCompletionTokens,
			UserDefined:                true,
		})
	}
	for _, m := range models {
		owned := m.OwnedBy
		if owned == "" {
			owned = ProviderName
		}
		display := m.DisplayName
		if display == "" {
			display = freebuff.DeriveDisplayName(m.ID)
		}
		add(m.ID, owned, display)
		if i := strings.LastIndex(m.ID, "/"); i >= 0 {
			add(m.ID[i+1:], owned, display)
		}
	}
	return out
}

// ----- auth -----

func (d *Dispatcher) HandleParseAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	name := strings.ToLower(req.FileName)
	sa, err := freebuff.ParseAuthStorage(req.RawJSON)
	if err != nil || len(sa.TokenList()) == 0 {
		if strings.Contains(name, "freebuff") {
			return nil, fmt.Errorf("freebuff auth file missing token")
		}
		return OkEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}
	if req.Provider != "" && req.Provider != ProviderName && !strings.Contains(name, "freebuff") {
		return OkEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}
	return OkEnvelope(pluginapi.AuthParseResponse{
		Handled: true,
		Auth:    ToAuthData(sa),
	})
}

func ToAuthData(sa freebuff.AuthStorage) pluginapi.AuthData {
	if len(sa.Tokens) == 0 {
		sa.Tokens = sa.TokenList()
	}
	if sa.Token == "" && len(sa.Tokens) > 0 {
		sa.Token = strings.Join(sa.Tokens, ",")
	}
	storage, _ := json.Marshal(sa)
	label := sa.Label
	if label == "" {
		label = "Freebuff"
		if n := len(sa.TokenList()); n > 1 {
			label = fmt.Sprintf("Freebuff (%d tokens)", n)
		}
	}
	return pluginapi.AuthData{
		Provider:    ProviderName,
		ID:          ProviderName,
		FileName:    AuthFileName,
		Label:       label,
		StorageJSON: storage,
		Metadata:    map[string]any{"type": ProviderName, "token_count": len(sa.TokenList())},
	}
}

func (d *Dispatcher) HandleStartLogin(raw []byte) ([]byte, error) {
	mode := freebuff.LoginModeFreebuff
	// Optional metadata / plugin config: {"mode":"codebuff"} or Host fields.
	var loose map[string]any
	_ = json.Unmarshal(raw, &loose)
	if m, ok := stringFromAny(loose["mode"]); ok {
		mode = freebuff.LoginMode(m)
	}
	if meta, ok := loose["Metadata"].(map[string]any); ok {
		if m, ok := stringFromAny(meta["mode"]); ok {
			mode = freebuff.LoginMode(m)
		}
	}
	if meta, ok := loose["metadata"].(map[string]any); ok {
		if m, ok := stringFromAny(meta["mode"]); ok {
			mode = freebuff.LoginMode(m)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := freebuff.StartCLILogin(ctx, mode, "")
	if err != nil {
		return nil, fmt.Errorf("login start: %w", err)
	}
	// State is the fingerprint id — host passes it back on poll.
	d.loginSessions.Store(session.FingerprintID, session)
	return OkEnvelope(pluginapi.AuthLoginStartResponse{
		Provider:  ProviderName,
		URL:       session.LoginURL,
		State:     session.FingerprintID,
		ExpiresAt: time.Now().Add(freebuff.LoginTTL).UTC(),
		Metadata: map[string]any{
			"mode":             string(session.Mode),
			"fingerprint_id":   session.FingerprintID,
			"fingerprint_hash": session.FingerprintHash,
			"instructions":     "Open the login URL in a browser, complete auth, then wait for CPA to poll. Token is saved as freebuff.json.",
		},
	})
}

func (d *Dispatcher) HandlePollLogin(raw []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	state := strings.TrimSpace(req.State)
	if state == "" {
		// Fallback loose field names.
		var loose map[string]any
		_ = json.Unmarshal(raw, &loose)
		state, _ = stringFromAny(loose["state"])
		if state == "" {
			state, _ = stringFromAny(loose["State"])
		}
	}
	if state == "" {
		return nil, fmt.Errorf("poll: empty state")
	}
	v, ok := d.loginSessions.Load(state)
	if !ok {
		return nil, fmt.Errorf("poll: unknown state (restart login)")
	}
	session := v.(*freebuff.LoginSession)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user, pending, err := freebuff.PollCLILogin(ctx, session, "")
	if err != nil {
		d.loginSessions.Delete(state)
		return OkEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: err.Error(),
		})
	}
	if pending {
		return OkEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "waiting for browser login",
		})
	}

	// Verify token against Freebuff session API before accepting.
	vr := freebuff.VerifyToken(ctx, user.AuthToken, "")
	if !vr.OK {
		d.loginSessions.Delete(state)
		return OkEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "token rejected: " + vr.Info,
		})
	}

	sa := freebuff.AuthStorageFromToken(user.AuthToken, user, session.Mode)
	d.loginSessions.Delete(state)
	return OkEnvelope(pluginapi.AuthLoginPollResponse{
		Status: pluginapi.AuthLoginStatusSuccess,
		Auth:   ToAuthData(sa),
	})
}

func stringFromAny(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

func (d *Dispatcher) HandleRefreshAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	sa, err := freebuff.ParseAuthStorage(req.StorageJSON)
	if err != nil || len(sa.TokenList()) == 0 {
		return nil, fmt.Errorf("refresh: invalid freebuff auth")
	}
	return OkEnvelope(pluginapi.AuthRefreshResponse{Auth: ToAuthData(sa)})
}

// ----- executor -----

func (d *Dispatcher) HandleExecExecute(raw []byte) ([]byte, error) {
	var req pluginapi.ExecutorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	completion, err := d.runCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	return OkEnvelope(pluginapi.ExecutorResponse{Payload: completion})
}

func (d *Dispatcher) HandleExecStream(raw []byte) ([]byte, error) {
	var req ExecutorStreamRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	headers := StreamHeaders()
	sseFramed := ClientNeedsSSEFrame(req.Metadata)

	if req.StreamID == "" {
		chunks, err := d.runStreamSync(req.ExecutorRequest, sseFramed)
		if err != nil {
			return nil, err
		}
		return OkEnvelope(StreamResponse{Headers: headers, Chunks: chunks})
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := d.runStreamAsync(ctx, req.ExecutorRequest, req.StreamID, sseFramed); err != nil {
			d.Host.StreamEmitError(req.StreamID, err.Error())
		}
		d.Host.StreamClose(req.StreamID)
	}()
	return OkEnvelope(StreamResponse{Headers: headers})
}

func StreamHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	return h
}

func ClientNeedsSSEFrame(metadata map[string]any) bool {
	path, _ := metadata["request_path"].(string)
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "/v1/chat/completions", "/v1/completions":
		return false
	default:
		return true
	}
}

func (d *Dispatcher) runCompletion(ctx context.Context, req pluginapi.ExecutorRequest) ([]byte, error) {
	prepared, cleanup, err := d.prepareRun(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	acc := freebuff.NewCompletionAccumulator(prepared.displayModel)
	var messageID string
	err = prepared.client.ChatEvents(ctx, prepared.payload, func(ev freebuff.ChatEvent) error {
		if ev.Done || ev.Object == nil {
			return nil
		}
		if id, ok := ev.Object["id"].(string); ok && id != "" {
			messageID = id
		}
		acc.Add(ev.Object)
		return nil
	})
	freebuff.FinalizeRun(context.Background(), prepared.client, prepared.run, messageID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(acc.FinalResponse())
}

func (d *Dispatcher) runStreamSync(req pluginapi.ExecutorRequest, sseFramed bool) ([]pluginapi.ExecutorStreamChunk, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	prepared, cleanup, err := d.prepareRun(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var chunks []pluginapi.ExecutorStreamChunk
	var messageID string
	err = prepared.client.ChatEvents(ctx, prepared.payload, func(ev freebuff.ChatEvent) error {
		if ev.Done || ev.Object == nil {
			return nil
		}
		if id, ok := ev.Object["id"].(string); ok && id != "" {
			messageID = id
		}
		clean := freebuff.SanitizeStreamChunk(ev.Object)
		if clean == nil {
			return nil
		}
		payload, err := json.Marshal(clean)
		if err != nil {
			return nil
		}
		if sseFramed {
			payload = append([]byte("data: "), payload...)
		}
		chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: payload})
		return nil
	})
	freebuff.FinalizeRun(context.Background(), prepared.client, prepared.run, messageID)
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

func (d *Dispatcher) runStreamAsync(ctx context.Context, req pluginapi.ExecutorRequest, streamID string, sseFramed bool) error {
	prepared, cleanup, err := d.prepareRun(ctx, req)
	if err != nil {
		return err
	}
	defer cleanup()

	var messageID string
	err = prepared.client.ChatEvents(ctx, prepared.payload, func(ev freebuff.ChatEvent) error {
		if ev.Done || ev.Object == nil {
			return nil
		}
		if id, ok := ev.Object["id"].(string); ok && id != "" {
			messageID = id
		}
		clean := freebuff.SanitizeStreamChunk(ev.Object)
		if clean == nil {
			return nil
		}
		payload, err := json.Marshal(clean)
		if err != nil {
			return nil
		}
		if sseFramed {
			payload = append([]byte("data: "), payload...)
		}
		return d.Host.StreamEmit(streamID, payload)
	})
	freebuff.FinalizeRun(context.Background(), prepared.client, prepared.run, messageID)
	return err
}

type preparedExec struct {
	client       *freebuff.Client
	run          freebuff.Run
	payload      map[string]any
	displayModel string
}

func (d *Dispatcher) prepareRun(ctx context.Context, req pluginapi.ExecutorRequest) (*preparedExec, func(), error) {
	sa, err := freebuff.ParseAuthStorage(req.StorageJSON)
	if err != nil || len(sa.TokenList()) == 0 {
		return nil, nil, fmt.Errorf("invalid freebuff auth storage")
	}
	pool, err := d.getPool(sa, "")
	if err != nil {
		return nil, nil, err
	}

	body, err := freebuff.ParseClientBody(req.Payload, req.OriginalRequest)
	if err != nil {
		return nil, nil, err
	}
	requested := req.Model
	if requested == "" {
		requested, _ = body["model"].(string)
	}
	model, ok := freebuff.ResolveModel(requested, d.ActiveModels())
	if !ok {
		model = freebuff.Model{
			ID:          requested,
			AgentID:     freebuff.MapModelToAgentID(requested),
			DisplayName: freebuff.DeriveDisplayName(requested),
		}
	}
	displayModel := requested
	if displayModel == "" {
		displayModel = model.ID
	}
	body["model"] = model.ID

	messages := freebuff.ExtractMessages(body)
	lease, err := pool.AcquireSession(ctx, model.SessionID(), messages)
	if err != nil {
		return nil, nil, err
	}
	client := lease.Client

	client.RequestAdChain(ctx, messages, "", false)
	_ = client.ValidateAgents(ctx, d.ActiveModels())

	run, err := freebuff.StartRunChain(ctx, client, model)
	if err != nil {
		lease.Release()
		return nil, nil, err
	}

	payload := freebuff.BuildUpstreamPayload(
		body,
		lease.Session,
		run.PayloadRunID(),
		client.Settings().ClientID,
		"",
		model.UpstreamID(),
	)

	return &preparedExec{
		client:       client,
		run:          run,
		payload:      payload,
		displayModel: displayModel,
	}, func() { lease.Release() }, nil
}

func (d *Dispatcher) getPool(sa freebuff.AuthStorage, hostProxy string) (*freebuff.AccountPool, error) {
	key := poolCacheKey(sa)
	d.poolCacheMu.Lock()
	defer d.poolCacheMu.Unlock()
	if p, ok := d.poolCache[key]; ok {
		return p, nil
	}
	p, err := freebuff.NewAccountPool(sa, hostProxy)
	if err != nil {
		return nil, err
	}
	d.poolCache[key] = p
	go d.discoverModels(p)
	return p, nil
}

func poolCacheKey(sa freebuff.AuthStorage) string {
	return strings.Join(sa.TokenList(), "|") + "|" + sa.APIBaseURL + "|" + sa.ProxyURL
}

func (d *Dispatcher) discoverModels(p *freebuff.AccountPool) {
	client := p.DefaultClient()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := client.FetchAvailableModels(ctx)
	if err != nil || len(models) == 0 {
		return
	}
	d.dynamicModelsMu.Lock()
	d.dynamicModels = models
	d.dynamicModelsMu.Unlock()
	d.Host.Log("info", fmt.Sprintf("freebuff discovered %d upstream models", len(models)))
}

// ----- envelopes -----

func OkEnvelope(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{OK: true, Result: raw})
}

func ErrorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(Envelope{OK: false, Error: &EnvelopeError{Code: code, Message: message}})
	return raw
}
