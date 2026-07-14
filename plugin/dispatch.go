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

// Identity selects which CPA auth-provider face this binary exposes.
//
// CPA maps one dynamic library → one auth.identifier, so Freebuff OAuth and
// Codebuff OAuth must be two libraries (freebuff.* and codebuff.*). Both share
// this package; build with:
//
//	// Freebuff (default): models + executor + Freebuff OAuth
//	go build -buildmode=c-shared -o freebuff.so .
//
//	// Codebuff: Codebuff OAuth only (credentials still execute via freebuff)
//	go build -buildmode=c-shared -ldflags "-X github.com/WslzGmzs/freebuff2api/plugin.Identity=codebuff" -o codebuff.so .
const (
	IdentityFreebuff = "freebuff"
	IdentityCodebuff = "codebuff"

	// RuntimeProvider is the executor/model provider key used in AuthData so
	// Codebuff OAuth credentials still route chat to the freebuff executor.
	RuntimeProvider = "freebuff"
	AuthFileName    = "freebuff.json"
)

// PluginVer is the plugin release version (no leading "v").
// Overridden at link time: -X github.com/WslzGmzs/freebuff2api/plugin.PluginVer=x.y.z
var PluginVer = "0.1.0"

// Identity is freebuff (default) or codebuff. Set via -X at link time.
var Identity = IdentityFreebuff

// ProviderName is the CPA auth.identifier for this binary (and legacy alias).
// Prefer AuthIdentifier() for new code.
var ProviderName = IdentityFreebuff

func init() {
	// Keep ProviderName in sync when Identity is injected via ldflags before init.
	ProviderName = AuthIdentifier()
}

// AuthIdentifier is the CPA auth.identifier / /v0/management/<id>-auth-url key.
func AuthIdentifier() string {
	switch strings.ToLower(strings.TrimSpace(Identity)) {
	case IdentityCodebuff:
		return IdentityCodebuff
	default:
		return IdentityFreebuff
	}
}

// IsCodebuffIdentity reports whether this binary is the Codebuff OAuth face.
func IsCodebuffIdentity() bool {
	return AuthIdentifier() == IdentityCodebuff
}

// DefaultLoginMode is the CLI login host for this binary's OAuth entry.
func DefaultLoginMode() freebuff.LoginMode {
	if IsCodebuffIdentity() {
		return freebuff.LoginModeCodebuff
	}
	return freebuff.LoginModeFreebuff
}

// OAuthDisplayName is the human-readable OAuth entry (CPA /oauth list label).
func OAuthDisplayName() string {
	if IsCodebuffIdentity() {
		return "Codebuff OAuth"
	}
	return "Freebuff OAuth"
}

// PluginDisplayName is metadata.Name for the library.
func PluginDisplayName() string {
	if IsCodebuffIdentity() {
		return "Codebuff"
	}
	return "Freebuff"
}

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
	id := AuthIdentifier()
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return OkEnvelope(d.Registration())
	case pluginabi.MethodModelStatic, pluginabi.MethodModelForAuth:
		if IsCodebuffIdentity() {
			// Codebuff face is auth-only; avoid duplicate model registration.
			return OkEnvelope(pluginapi.ModelResponse{Provider: RuntimeProvider, Models: nil})
		}
		return OkEnvelope(pluginapi.ModelResponse{Provider: RuntimeProvider, Models: d.Models()})
	case pluginabi.MethodModelRegister:
		if IsCodebuffIdentity() {
			return OkEnvelope(pluginapi.ModelRegistrationResponse{Provider: RuntimeProvider, Models: nil})
		}
		return OkEnvelope(pluginapi.ModelRegistrationResponse{Provider: RuntimeProvider, Models: d.Models()})
	case pluginabi.MethodAuthIdentifier:
		return OkEnvelope(IdentifierResponse{Identifier: id})
	case pluginabi.MethodAuthParse:
		return d.HandleParseAuth(request)
	case pluginabi.MethodAuthLoginStart:
		return d.HandleStartLogin(request)
	case pluginabi.MethodAuthLoginPoll:
		return d.HandlePollLogin(request)
	case pluginabi.MethodAuthRefresh:
		return d.HandleRefreshAuth(request)
	case pluginabi.MethodExecutorIdentifier:
		// Executor always owns the freebuff runtime provider id.
		return OkEnvelope(IdentifierResponse{Identifier: RuntimeProvider})
	case pluginabi.MethodExecutorExecute:
		if IsCodebuffIdentity() {
			return nil, fmt.Errorf("codebuff plugin is auth-only; install freebuff.* for chat execution")
		}
		return d.HandleExecExecute(request)
	case pluginabi.MethodExecutorExecuteStream:
		if IsCodebuffIdentity() {
			return nil, fmt.Errorf("codebuff plugin is auth-only; install freebuff.* for chat execution")
		}
		return d.HandleExecStream(request)
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
	// Freebuff library: models + executor + Freebuff OAuth.
	// Codebuff library: Codebuff OAuth only (same chat stack via freebuff.*).
	caps := RegistrationCapability{
		AuthProvider: true,
	}
	fields := []pluginapi.ConfigField{
		{
			Name:        "debug",
			Type:        pluginapi.ConfigFieldTypeBoolean,
			Description: "Enable verbose Freebuff upstream logging (also settable per-credential via freebuff.json debug).",
		},
	}
	if !IsCodebuffIdentity() {
		caps.ModelProvider = true
		caps.Executor = true
		caps.ExecutorModelScope = pluginapi.ExecutorModelScopeBoth
		caps.ExecutorInputFormats = []string{"chat-completions"}
		caps.ExecutorOutputFormats = []string{"chat-completions"}
		fields = append(fields, pluginapi.ConfigField{
			Name:        "login_mode",
			Type:        pluginapi.ConfigFieldTypeEnum,
			EnumValues:  []string{"freebuff", "codebuff"},
			Description: "Optional override for Freebuff binary OAuth host (default freebuff). Prefer the separate codebuff.* plugin for Codebuff OAuth on /oauth.",
		})
	}

	return Registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			// CPA /oauth entry comes from auth.identifier (+ management UI labels).
			Name:             PluginDisplayName(),
			Version:          PluginVer,
			Author:           "WslzGmzs",
			GitHubRepository: "https://github.com/WslzGmzs/freebuff2api",
			ConfigFields:     fields,
		},
		Capabilities: caps,
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

// ----- auth (CPA /oauth via auth.login.*) -----

func (d *Dispatcher) HandleParseAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	name := strings.ToLower(req.FileName)
	sa, err := freebuff.ParseAuthStorage(req.RawJSON)
	if err != nil || len(sa.TokenList()) == 0 {
		if strings.Contains(name, "freebuff") || strings.Contains(name, "codebuff") {
			return nil, fmt.Errorf("freebuff auth file missing token")
		}
		return OkEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}

	// Freebuff library owns freebuff credentials; codebuff library only claims
	// codebuff-tagged files (login_mode/provider/filename), so both can coexist.
	if !authFileBelongsToIdentity(req.Provider, name, sa) {
		return OkEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}

	// Prefer host-configured proxy from auth dir / host summary when file omits proxy_url.
	if sa.ProxyURL == "" && req.Host.ProxyURL != "" {
		sa.ProxyURL = req.Host.ProxyURL
	}
	return OkEnvelope(pluginapi.AuthParseResponse{
		Handled: true,
		Auth:    ToAuthData(sa),
	})
}

// authFileBelongsToIdentity decides whether this binary should claim an auth file.
// Freebuff and Codebuff credentials are strictly partitioned by filename / login_mode /
// credential_source so they never share the same auth file on disk.
func authFileBelongsToIdentity(reqProvider, fileName string, sa freebuff.AuthStorage) bool {
	reqProvider = strings.ToLower(strings.TrimSpace(reqProvider))
	fileName = strings.ToLower(strings.TrimSpace(fileName))
	mode := strings.ToLower(strings.TrimSpace(sa.LoginMode))
	src := strings.ToLower(sa.CredentialSource())
	if srcAttr, ok := sa.Attributes["credential_source"]; ok {
		if s := strings.ToLower(strings.TrimSpace(srcAttr)); s != "" {
			src = s
		}
	}
	isCodebuffFile := strings.Contains(fileName, "codebuff") ||
		mode == IdentityCodebuff ||
		src == IdentityCodebuff ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(sa.ID)), "codebuff-")
	isFreebuffFile := strings.Contains(fileName, "freebuff") ||
		(!isCodebuffFile && (mode == IdentityFreebuff || mode == "" || src == IdentityFreebuff))

	if IsCodebuffIdentity() {
		// Codebuff OAuth plugin only owns codebuff-namespaced credentials.
		if reqProvider == IdentityCodebuff {
			return true
		}
		return isCodebuffFile
	}

	// Freebuff plugin never claims codebuff-namespaced files (separate credential store).
	if isCodebuffFile {
		return false
	}
	if reqProvider != "" && reqProvider != IdentityFreebuff && reqProvider != IdentityCodebuff {
		return isFreebuffFile || strings.Contains(fileName, "freebuff")
	}
	return true
}

// credentialFileName picks a distinct on-disk name for freebuff vs codebuff credentials.
//
//	freebuff → freebuff.json or freebuff-<hash>.json
//	codebuff → codebuff.json or codebuff-<hash>.json
func credentialFileName(sa freebuff.AuthStorage) string {
	base := sa.AuthFileName() // freebuff.json | codebuff.json
	id := strings.TrimSpace(sa.ID)
	if id == "" || id == IdentityFreebuff || id == IdentityCodebuff {
		return base
	}
	// Prefer id-based unique files; id is already freebuff-*/codebuff-* prefixed.
	if strings.HasSuffix(strings.ToLower(id), ".json") {
		return id
	}
	return id + ".json"
}

// ToAuthData maps freebuff.json / codebuff.json (+ host fields) onto pluginapi.AuthData.
// Provider is always RuntimeProvider (freebuff) so chat uses freebuff executor.
// FileName / ID stay namespaced so Freebuff and Codebuff OAuth never overwrite each other.
func ToAuthData(sa freebuff.AuthStorage) pluginapi.AuthData {
	sa.Normalize()
	// StorageJSON keeps Freebuff-owned fields; host also gets standard AuthData fields.
	storage, _ := json.Marshal(sa)
	fileName := credentialFileName(sa)
	if sa.Metadata == nil {
		sa.Metadata = map[string]any{}
	}
	sa.Metadata["type"] = RuntimeProvider
	sa.Metadata["login_mode"] = sa.LoginMode
	sa.Metadata["credential_source"] = sa.CredentialSource()
	if sa.Attributes == nil {
		sa.Attributes = map[string]string{}
	}
	sa.Attributes["credential_source"] = sa.CredentialSource()
	sa.Attributes["login_mode"] = sa.LoginMode
	return pluginapi.AuthData{
		// Always freebuff so model/executor routing hits freebuff.*.
		Provider:    RuntimeProvider,
		ID:          sa.ID,
		FileName:    fileName,
		Label:       sa.Label,
		Prefix:      sa.Prefix,
		ProxyURL:    sa.ProxyURL,
		Disabled:    sa.Disabled,
		StorageJSON: storage,
		Metadata:    sa.Metadata,
		Attributes:  sa.Attributes,
	}
}

func (d *Dispatcher) HandleStartLogin(raw []byte) ([]byte, error) {
	mode, proxy := resolveLoginModeAndProxy(raw)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := freebuff.StartCLILogin(ctx, mode, proxy)
	if err != nil {
		return nil, fmt.Errorf("login start: %w", err)
	}
	// State is the fingerprint id — host passes it back on poll.
	d.loginSessions.Store(session.FingerprintID, session)

	oauthName := OAuthDisplayName()
	if session.Mode == freebuff.LoginModeCodebuff {
		oauthName = "Codebuff OAuth"
	} else {
		oauthName = "Freebuff OAuth"
	}
	return OkEnvelope(pluginapi.AuthLoginStartResponse{
		// Must match auth.identifier so CPA routes poll to this library.
		Provider:  AuthIdentifier(),
		URL:       session.LoginURL,
		State:     session.FingerprintID,
		ExpiresAt: time.Now().Add(freebuff.LoginTTL).UTC(),
		Metadata: map[string]any{
			"mode":             string(session.Mode),
			"oauth_name":       oauthName,
			"name":             oauthName,
			"label":            oauthName,
			"fingerprint_id":   session.FingerprintID,
			"fingerprint_hash": session.FingerprintHash,
			"instructions":     "Complete " + oauthName + " in the browser. CPA will poll until the token is ready.",
		},
	})
}

func (d *Dispatcher) HandlePollLogin(raw []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	state := strings.TrimSpace(req.State)
	proxy := ""
	if state == "" {
		var loose map[string]any
		_ = json.Unmarshal(raw, &loose)
		state, _ = stringFromAny(loose["state"])
		if state == "" {
			state, _ = stringFromAny(loose["State"])
		}
	}
	if req.Host.ProxyURL != "" {
		proxy = req.Host.ProxyURL
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
	user, pending, err := freebuff.PollCLILogin(ctx, session, proxy)
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

	vr := freebuff.VerifyToken(ctx, user.AuthToken, proxy)
	if !vr.OK {
		d.loginSessions.Delete(state)
		return OkEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "token rejected: " + vr.Info,
		})
	}

	sa := freebuff.AuthStorageFromToken(user.AuthToken, user, session.Mode)
	if proxy != "" && sa.ProxyURL == "" {
		sa.ProxyURL = proxy
	}
	d.loginSessions.Delete(state)
	return OkEnvelope(pluginapi.AuthLoginPollResponse{
		Status: pluginapi.AuthLoginStatusSuccess,
		Auth:   ToAuthData(sa),
	})
}

// resolveLoginModeAndProxy reads mode from AuthLoginStartRequest / loose JSON.
// Binary identity sets the default (codebuff.* → always Codebuff OAuth).
func resolveLoginModeAndProxy(raw []byte) (freebuff.LoginMode, string) {
	mode := DefaultLoginMode()
	proxy := ""

	var req pluginapi.AuthLoginStartRequest
	if err := json.Unmarshal(raw, &req); err == nil {
		if req.Host.ProxyURL != "" {
			proxy = req.Host.ProxyURL
		}
		// Request Provider from CPA is the auth.identifier (freebuff|codebuff).
		if p := strings.ToLower(strings.TrimSpace(req.Provider)); p != "" {
			mode = loginModeFromOAuthLabel(p)
		}
		if req.Metadata != nil {
			if m, ok := stringFromAny(req.Metadata["mode"]); ok {
				mode = freebuff.LoginMode(m)
			} else if m, ok := stringFromAny(req.Metadata["login_mode"]); ok {
				mode = freebuff.LoginMode(m)
			} else if m, ok := stringFromAny(req.Metadata["oauth"]); ok {
				mode = loginModeFromOAuthLabel(m)
			} else if m, ok := stringFromAny(req.Metadata["name"]); ok {
				mode = loginModeFromOAuthLabel(m)
			}
		}
	}

	var loose map[string]any
	_ = json.Unmarshal(raw, &loose)
	if m, ok := stringFromAny(loose["mode"]); ok {
		mode = freebuff.LoginMode(m)
	}
	if m, ok := stringFromAny(loose["login_mode"]); ok {
		mode = freebuff.LoginMode(m)
	}
	if meta, ok := loose["Metadata"].(map[string]any); ok {
		if m, ok := stringFromAny(meta["mode"]); ok {
			mode = freebuff.LoginMode(m)
		} else if m, ok := stringFromAny(meta["login_mode"]); ok {
			mode = freebuff.LoginMode(m)
		} else if m, ok := stringFromAny(meta["oauth"]); ok {
			mode = loginModeFromOAuthLabel(m)
		} else if m, ok := stringFromAny(meta["name"]); ok {
			mode = loginModeFromOAuthLabel(m)
		}
	}
	if meta, ok := loose["metadata"].(map[string]any); ok {
		if m, ok := stringFromAny(meta["mode"]); ok {
			mode = freebuff.LoginMode(m)
		} else if m, ok := stringFromAny(meta["login_mode"]); ok {
			mode = freebuff.LoginMode(m)
		} else if m, ok := stringFromAny(meta["oauth"]); ok {
			mode = loginModeFromOAuthLabel(m)
		} else if m, ok := stringFromAny(meta["name"]); ok {
			mode = loginModeFromOAuthLabel(m)
		}
	}
	if m, ok := stringFromAny(loose["provider"]); ok {
		mode = loginModeFromOAuthLabel(m)
	}
	if m, ok := stringFromAny(loose["Provider"]); ok {
		mode = loginModeFromOAuthLabel(m)
	}

	// Codebuff binary always uses Codebuff OAuth regardless of overrides.
	if IsCodebuffIdentity() {
		mode = freebuff.LoginModeCodebuff
	}

	if mode != freebuff.LoginModeFreebuff && mode != freebuff.LoginModeCodebuff {
		mode = DefaultLoginMode()
	}
	return mode, proxy
}

func loginModeFromOAuthLabel(s string) freebuff.LoginMode {
	l := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(l, "codebuff"):
		return freebuff.LoginModeCodebuff
	case strings.Contains(l, "freebuff"):
		return freebuff.LoginModeFreebuff
	default:
		return freebuff.LoginModeFreebuff
	}
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
	if sa.ProxyURL == "" && req.Host.ProxyURL != "" {
		sa.ProxyURL = req.Host.ProxyURL
	}
	// Bearer tokens have no refresh endpoint; re-verify optionally and echo.
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
	if sa.Disabled {
		return nil, nil, fmt.Errorf("freebuff auth is disabled")
	}
	// Host AuthData.ProxyURL / attributes may override empty storage proxy.
	hostProxy := ""
	if sa.ProxyURL == "" {
		if req.AuthAttributes != nil {
			if p := req.AuthAttributes["proxy_url"]; p != "" {
				hostProxy = p
			}
		}
		if hostProxy == "" && req.AuthMetadata != nil {
			if p, ok := req.AuthMetadata["proxy_url"].(string); ok {
				hostProxy = p
			}
		}
	}
	pool, err := d.getPool(sa, hostProxy)
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
