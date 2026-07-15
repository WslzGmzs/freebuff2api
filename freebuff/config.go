package freebuff

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL      = "https://www.codebuff.com"
	DefaultZeroClickURL = "https://zeroclick.dev"
	DefaultTimeout      = 60 * time.Second
	BrowserUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	JSONUserAgent       = "Bun/1.3.11"
	CLIUserAgent        = "Freebuff-CLI/0.0.105"
	ChatUserAgent       = "ai-sdk/openai-compatible/0.0.0-test/codebuff ai-sdk/provider-utils/3.0.20 runtime/browser"
)

// Settings holds Freebuff/Codebuff client configuration for one account.
type Settings struct {
	Token        string
	BaseURL      string
	ZeroClickURL string
	SessionID    string
	ClientID     string
	AdProviders  []string
	Timeout      time.Duration
	ProxyURL     string // empty = direct; never trusts env proxy
	Timezone     string
	Locale       string
	OSName       string
	Debug        bool
}

// ModelAlias matches CPA OAuthModelAlias JSON (name=upstream, alias=client-facing).
type ModelAlias struct {
	Name         string `json:"name"`
	Alias        string `json:"alias"`
	ForceMapping bool   `json:"force-mapping,omitempty"`
	Fork         bool   `json:"fork,omitempty"`
}

// AuthStorage is the on-disk freebuff.json / codebuff.json shape aligned with
// CPA Auth fields plus Freebuff-specific storage.
//
// Host-standard fields (also accepted at top level of the auth file):
//
//	id, provider, prefix, label, disabled, proxy_url, priority,
//	excluded_models, model_aliases, attributes, metadata, ...
//
// Freebuff-owned fields:
//
//	token / tokens, api_base_url, ad_providers, timezone, locale, os, debug,
//	login_mode (freebuff|codebuff), zeroclick_base_url, session_id, client_id
//
// See CPA sdk/cliproxy Auth and pluginapi.AuthData. Mirrors workbuddy-cli-proxy
// credential fields so CPA oauth-excluded-models / model-aliases / disabled work.
type AuthStorage struct {
	// --- CPA host-facing fields (persisted in auth file) ---
	ID         string            `json:"id,omitempty"`
	Provider   string            `json:"provider,omitempty"`
	Prefix     string            `json:"prefix,omitempty"`
	Label      string            `json:"label,omitempty"`
	Disabled   bool              `json:"disabled,omitempty"`
	ProxyURL   string            `json:"proxy_url,omitempty"`
	Priority   int               `json:"priority,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Metadata   map[string]any    `json:"metadata,omitempty"`

	// Per-credential model policy (CPA panel / config / PATCH).
	ExcludedModels       []string     `json:"excluded_models,omitempty"`
	ExcludedModelsHyphen []string     `json:"excluded-models,omitempty"`
	ModelAliases         []ModelAlias `json:"model_aliases,omitempty"`
	ModelAliasesHyphen   []ModelAlias `json:"model-aliases,omitempty"`

	// --- Freebuff provider storage ---
	Token       string   `json:"token,omitempty"`
	Tokens      []string `json:"tokens,omitempty"`
	APIBaseURL  string   `json:"api_base_url,omitempty"`
	ZeroClick   string   `json:"zeroclick_base_url,omitempty"`
	AdProviders []string `json:"ad_providers,omitempty"`
	Timezone    string   `json:"timezone,omitempty"`
	Locale      string   `json:"locale,omitempty"`
	OS          string   `json:"os,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	ClientID    string   `json:"client_id,omitempty"`
	Debug       bool     `json:"debug,omitempty"`
	// LoginMode records which CLI host issued the token: freebuff | codebuff.
	LoginMode string `json:"login_mode,omitempty"`
	// Type is a legacy alias some hosts store (e.g. "freebuff").
	Type string `json:"type,omitempty"`
}

// TokenList returns unique non-empty Freebuff bearer tokens.
func (a AuthStorage) TokenList() []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(raw string) {
		for _, part := range strings.Split(raw, ",") {
			t := strings.TrimSpace(part)
			t = strings.TrimPrefix(t, "Bearer ")
			t = strings.TrimPrefix(t, "bearer ")
			if t == "" {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	add(a.Token)
	for _, t := range a.Tokens {
		add(t)
	}
	// Nested metadata.token fallback
	if len(out) == 0 && a.Metadata != nil {
		if v, ok := a.Metadata["token"].(string); ok {
			add(v)
		}
		if v, ok := a.Metadata["access_token"].(string); ok {
			add(v)
		}
	}
	return out
}

// ParseAuthStorage decodes freebuff credential JSON (full CPA auth file or storage-only).
func ParseAuthStorage(raw []byte) (AuthStorage, error) {
	var a AuthStorage
	if err := json.Unmarshal(raw, &a); err != nil {
		return AuthStorage{}, err
	}
	// Loose map for alternate key casings / nested shapes.
	if len(a.TokenList()) == 0 {
		var loose map[string]any
		if err := json.Unmarshal(raw, &loose); err == nil {
			for _, key := range []string{
				"token", "access_token", "accessToken", "FREEBUFF_TOKEN", "codebuff_token", "authToken",
			} {
				if v, ok := loose[key].(string); ok && strings.TrimSpace(v) != "" {
					a.Token = v
					break
				}
			}
			// Nested auth.accessToken (unlikely for freebuff but harmless)
			if len(a.TokenList()) == 0 {
				if auth, ok := loose["auth"].(map[string]any); ok {
					if v, ok := auth["accessToken"].(string); ok {
						a.Token = v
					} else if v, ok := auth["token"].(string); ok {
						a.Token = v
					}
				}
			}
			if a.ProxyURL == "" {
				if v, ok := loose["proxy_url"].(string); ok {
					a.ProxyURL = v
				} else if v, ok := loose["ProxyURL"].(string); ok {
					a.ProxyURL = v
				}
			}
			if a.Prefix == "" {
				if v, ok := loose["prefix"].(string); ok {
					a.Prefix = v
				}
			}
			if a.Priority == 0 {
				switch v := loose["priority"].(type) {
				case float64:
					a.Priority = int(v)
				case string:
					if n, err := strconv.Atoi(v); err == nil {
						a.Priority = n
					}
				}
			}
		}
	}
	if a.Provider == "" {
		a.Provider = "freebuff"
	}
	if a.Type == "" {
		a.Type = "freebuff"
	}
	return a, nil
}

// CredentialSource is freebuff or codebuff — drives filename / id prefixes.
func (a AuthStorage) CredentialSource() string {
	mode := strings.ToLower(strings.TrimSpace(a.LoginMode))
	if mode == string(LoginModeCodebuff) {
		return string(LoginModeCodebuff)
	}
	// Filename / explicit type hints when login_mode missing.
	if strings.EqualFold(strings.TrimSpace(a.Type), string(LoginModeCodebuff)) {
		return string(LoginModeCodebuff)
	}
	return string(LoginModeFreebuff)
}

// IsCodebuffCredential reports whether this storage is a Codebuff OAuth/login credential.
func (a AuthStorage) IsCodebuffCredential() bool {
	return a.CredentialSource() == string(LoginModeCodebuff)
}

// AuthFileName returns the default on-disk credential file name for this storage.
// Freebuff → freebuff.json；Codebuff → codebuff.json（与 OAuth 入口分离）.
func (a AuthStorage) AuthFileName() string {
	if a.IsCodebuffCredential() {
		return "codebuff.json"
	}
	return "freebuff.json"
}

// Normalize prepares IDs/tokens before persist or AuthData conversion.
func (a *AuthStorage) Normalize() {
	if len(a.Tokens) == 0 {
		a.Tokens = a.TokenList()
	}
	if a.Token == "" && len(a.Tokens) > 0 {
		a.Token = strings.Join(a.Tokens, ",")
	}
	// login_mode defaults to freebuff unless already codebuff.
	if a.LoginMode == "" {
		if strings.EqualFold(strings.TrimSpace(a.Type), string(LoginModeCodebuff)) {
			a.LoginMode = string(LoginModeCodebuff)
		} else {
			a.LoginMode = string(LoginModeFreebuff)
		}
	}
	// Runtime provider for CPA executor routing is always freebuff.
	// Source of the credential (oauth host) is login_mode + attributes.credential_source.
	a.Provider = "freebuff"
	a.Type = "freebuff"
	a.Prefix = NormalizeAuthPrefix(a.Prefix)
	// Merge hyphenated panel keys into canonical snake_case.
	if len(a.ExcludedModels) == 0 && len(a.ExcludedModelsHyphen) > 0 {
		a.ExcludedModels = a.ExcludedModelsHyphen
	}
	if len(a.ModelAliases) == 0 && len(a.ModelAliasesHyphen) > 0 {
		a.ModelAliases = a.ModelAliasesHyphen
	}
	a.ExcludedModelsHyphen = nil
	a.ModelAliasesHyphen = nil
	a.ExcludedModels = CleanStringList(a.ExcludedModels)
	a.ModelAliases = CleanModelAliases(a.ModelAliases)

	src := a.CredentialSource()
	if a.ID == "" {
		a.ID = DeriveAuthID(a.TokenList(), src)
	} else {
		// Keep mode-prefixed ids stable if already set correctly; rewrite freebuff-→codebuff- when needed.
		a.ID = ensureIDPrefix(a.ID, src)
	}
	if a.Label == "" {
		a.Label = defaultLabel(*a)
	}
	if a.Metadata == nil {
		a.Metadata = map[string]any{}
	}
	a.Metadata["type"] = "freebuff"
	a.Metadata["token_count"] = len(a.TokenList())
	a.Metadata["login_mode"] = a.LoginMode
	a.Metadata["credential_source"] = src
	if a.Prefix != "" {
		a.Metadata["prefix"] = a.Prefix
	}
	if a.ProxyURL != "" {
		a.Metadata["proxy_url"] = a.ProxyURL
	}
	if a.Priority != 0 {
		a.Metadata["priority"] = a.Priority
	}
	if a.Disabled {
		a.Metadata["disabled"] = true
	} else {
		delete(a.Metadata, "disabled")
	}
	if len(a.ExcludedModels) > 0 {
		a.Metadata["excluded_models"] = append([]string(nil), a.ExcludedModels...)
		a.Metadata["excluded-models"] = append([]string(nil), a.ExcludedModels...)
	} else {
		delete(a.Metadata, "excluded_models")
		delete(a.Metadata, "excluded-models")
	}
	if len(a.ModelAliases) > 0 {
		a.Metadata["model_aliases"] = append([]ModelAlias(nil), a.ModelAliases...)
		a.Metadata["model-aliases"] = append([]ModelAlias(nil), a.ModelAliases...)
	} else {
		delete(a.Metadata, "model_aliases")
		delete(a.Metadata, "model-aliases")
	}
	if a.Attributes == nil {
		a.Attributes = map[string]string{}
	}
	a.Attributes["provider"] = "freebuff"
	a.Attributes["login_mode"] = a.LoginMode
	a.Attributes["credential_source"] = src
	// auth_kind helps CPA merge global oauth-excluded-models / oauth-model-alias.
	a.Attributes["auth_kind"] = "oauth"
	if a.Priority != 0 {
		a.Attributes["priority"] = strconv.Itoa(a.Priority)
	} else {
		delete(a.Attributes, "priority")
	}
	if a.Disabled {
		a.Attributes["disabled"] = "true"
	} else {
		delete(a.Attributes, "disabled")
	}
	if len(a.ExcludedModels) > 0 {
		a.Attributes["excluded_models"] = strings.Join(a.ExcludedModels, ",")
	} else {
		delete(a.Attributes, "excluded_models")
	}
	if len(a.ModelAliases) > 0 {
		if raw, err := json.Marshal(a.ModelAliases); err == nil {
			a.Attributes["model_aliases"] = string(raw)
		}
	} else {
		delete(a.Attributes, "model_aliases")
	}
}

func defaultLabel(a AuthStorage) string {
	n := len(a.TokenList())
	base := "Freebuff OAuth"
	if a.IsCodebuffCredential() {
		base = "Codebuff OAuth"
	}
	if n > 1 {
		return fmt.Sprintf("%s (%d tokens)", base, n)
	}
	return base
}

// DeriveAuthID builds a stable id from token material, namespaced by credential source.
// freebuff → freebuff-<hash>；codebuff → codebuff-<hash>（避免两边撞同一 id/文件）.
func DeriveAuthID(tokens []string, source string) string {
	src := strings.ToLower(strings.TrimSpace(source))
	if src != string(LoginModeCodebuff) {
		src = string(LoginModeFreebuff)
	}
	if len(tokens) == 0 {
		return src + "-" + randomHex(4)
	}
	// Include source in hash so the same token logged in via both OAuth faces
	// still gets distinct ids/files.
	h := sha256.Sum256([]byte(src + "|" + strings.Join(tokens, ",")))
	return src + "-" + hex.EncodeToString(h[:8])
}

func ensureIDPrefix(id, source string) string {
	id = strings.TrimSpace(id)
	src := strings.ToLower(strings.TrimSpace(source))
	if src != string(LoginModeCodebuff) {
		src = string(LoginModeFreebuff)
	}
	if id == "" {
		return src + "-" + randomHex(4)
	}
	// Already correctly namespaced.
	if strings.HasPrefix(strings.ToLower(id), src+"-") {
		return id
	}
	// Migrate freebuff-xxx → codebuff-xxx (or vice versa) by re-prefixing.
	for _, p := range []string{"freebuff-", "codebuff-"} {
		if strings.HasPrefix(strings.ToLower(id), p) {
			return src + "-" + id[len(p):]
		}
	}
	return src + "-" + id
}

// ToSettings builds client settings for a single token.
// hostProxy is used when storage proxy_url is empty (CPA AuthData.ProxyURL / host config).
func (a AuthStorage) ToSettings(token string, hostProxy string) Settings {
	base := strings.TrimRight(firstNonEmpty(a.APIBaseURL, DefaultBaseURL), "/")
	zc := strings.TrimRight(firstNonEmpty(a.ZeroClick, DefaultZeroClickURL), "/")
	providers := a.AdProviders
	if len(providers) == 0 {
		providers = []string{"gravity", "zeroclick"}
	}
	proxy := strings.TrimSpace(a.ProxyURL)
	if proxy == "" {
		proxy = strings.TrimSpace(hostProxy)
	}
	return Settings{
		Token:        token,
		BaseURL:      base,
		ZeroClickURL: zc,
		SessionID:    firstNonEmpty(a.SessionID, randomHex(16)),
		ClientID:     firstNonEmpty(a.ClientID, randomHex(6)),
		AdProviders:  providers,
		Timeout:      DefaultTimeout,
		ProxyURL:     proxy,
		Timezone:     firstNonEmpty(a.Timezone, "Asia/Shanghai"),
		Locale:       firstNonEmpty(a.Locale, "zh-CN"),
		OSName:       firstNonEmpty(a.OS, "windows"),
		Debug:        a.Debug,
	}
}

// AuthStorageFromToken builds a namespaced credential after successful OAuth/CLI login.
// Freebuff → freebuff.json / freebuff-<hash>；Codebuff → codebuff.json / codebuff-<hash>.
// Runtime provider stays freebuff so chat still hits the freebuff executor.
func AuthStorageFromToken(token string, user *LoginUser, mode LoginMode) AuthStorage {
	if mode != LoginModeCodebuff {
		mode = LoginModeFreebuff
	}
	label := "Freebuff OAuth"
	if mode == LoginModeCodebuff {
		label = "Codebuff OAuth"
	}
	if user != nil {
		if user.Name != "" {
			label = user.Name + " · " + label
		} else if user.Email != "" {
			label = user.Email + " · " + label
		}
	}
	src := string(mode)
	sa := AuthStorage{
		// Executor routing key (CPA).
		Provider:  "freebuff",
		Type:      "freebuff",
		Token:     token,
		Label:     label,
		LoginMode: string(mode),
		ID:        DeriveAuthID([]string{token}, src),
		Metadata: map[string]any{
			"type":              "freebuff",
			"login_mode":        string(mode),
			"credential_source": src,
			"oauth":             true,
		},
		Attributes: map[string]string{
			"provider":          "freebuff",
			"login_mode":        string(mode),
			"credential_source": src,
		},
	}
	if user != nil {
		if user.ID != "" {
			sa.Metadata["user_id"] = user.ID
		}
		if user.Email != "" {
			sa.Metadata["email"] = user.Email
		}
		if user.Name != "" {
			sa.Metadata["name"] = user.Name
		}
	}
	sa.Normalize()
	return sa
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte("freebufffallback"))[:nBytes*2]
	}
	return hex.EncodeToString(b)
}

// HostHeader returns the Host header value for a base URL.
func HostHeader(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "www.codebuff.com"
	}
	return u.Host
}

// NormalizeAuthPrefix trims CPA model prefix (no trailing slash).
func NormalizeAuthPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.TrimSuffix(prefix, "/")
	return strings.TrimSpace(prefix)
}

// CleanStringList dedupes and trims a string list (case-insensitive keys).
func CleanStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// CleanModelAliases validates CPA model alias entries.
func CleanModelAliases(in []ModelAlias) []ModelAlias {
	if len(in) == 0 {
		return nil
	}
	out := make([]ModelAlias, 0, len(in))
	seen := map[string]struct{}{}
	for _, a := range in {
		name := strings.TrimSpace(a.Name)
		alias := strings.TrimSpace(a.Alias)
		if name == "" || alias == "" || strings.EqualFold(name, alias) {
			continue
		}
		key := strings.ToLower(alias)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ModelAlias{
			Name:         name,
			Alias:        alias,
			ForceMapping: a.ForceMapping,
			Fork:         a.Fork,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ApplyHostCredentialFields copies CPA host-managed credential fields from
// refresh/parse/execute metadata when storage JSON omitted them (panel PATCH
// often updates metadata without rewriting plugin storage).
func ApplyHostCredentialFields(sa *AuthStorage, metadata map[string]any, attributes map[string]string) {
	if sa == nil {
		return
	}
	if metadata != nil {
		if sa.Prefix == "" {
			if v, ok := metadata["prefix"].(string); ok {
				sa.Prefix = NormalizeAuthPrefix(v)
			}
		}
		if sa.ProxyURL == "" {
			if v, ok := metadata["proxy_url"].(string); ok {
				sa.ProxyURL = strings.TrimSpace(v)
			}
		}
		if sa.Priority == 0 {
			if n, ok := anyToInt(metadata["priority"]); ok {
				sa.Priority = n
			}
		}
		if !sa.Disabled {
			if b, ok := metadata["disabled"].(bool); ok && b {
				sa.Disabled = true
			}
		}
		if len(sa.ExcludedModels) == 0 {
			if list := stringListFromAny(metadata["excluded_models"]); len(list) > 0 {
				sa.ExcludedModels = list
			} else if list := stringListFromAny(metadata["excluded-models"]); len(list) > 0 {
				sa.ExcludedModels = list
			}
		}
		if len(sa.ModelAliases) == 0 {
			if aliases := modelAliasesFromAny(metadata["model_aliases"]); len(aliases) > 0 {
				sa.ModelAliases = aliases
			} else if aliases := modelAliasesFromAny(metadata["model-aliases"]); len(aliases) > 0 {
				sa.ModelAliases = aliases
			}
		}
	}
	if attributes != nil {
		if sa.Priority == 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(attributes["priority"])); err == nil {
				sa.Priority = n
			}
		}
		if !sa.Disabled {
			if strings.EqualFold(strings.TrimSpace(attributes["disabled"]), "true") {
				sa.Disabled = true
			}
		}
		if sa.ProxyURL == "" {
			if v := strings.TrimSpace(attributes["proxy_url"]); v != "" {
				sa.ProxyURL = v
			}
		}
		if sa.Prefix == "" {
			if v := strings.TrimSpace(attributes["prefix"]); v != "" {
				sa.Prefix = NormalizeAuthPrefix(v)
			}
		}
		if len(sa.ExcludedModels) == 0 {
			if v := strings.TrimSpace(attributes["excluded_models"]); v != "" {
				sa.ExcludedModels = CleanStringList(strings.Split(v, ","))
			}
		}
		if len(sa.ModelAliases) == 0 {
			if v := strings.TrimSpace(attributes["model_aliases"]); v != "" {
				var aliases []ModelAlias
				if json.Unmarshal([]byte(v), &aliases) == nil {
					sa.ModelAliases = CleanModelAliases(aliases)
				}
			}
		}
	}
	sa.ExcludedModels = CleanStringList(sa.ExcludedModels)
	sa.ModelAliases = CleanModelAliases(sa.ModelAliases)
}

// EnsureModelAllowed rejects models listed in the credential's excluded_models
// (defense in depth; CPA host also filters the model registry).
func EnsureModelAllowed(modelID string, sa AuthStorage) error {
	if len(sa.ExcludedModels) == 0 {
		return nil
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	bare := modelID
	if sa.Prefix != "" {
		p := sa.Prefix
		if strings.HasPrefix(bare, p+"/") {
			bare = strings.TrimPrefix(bare, p+"/")
		} else if strings.HasPrefix(bare, p+"-") {
			bare = strings.TrimPrefix(bare, p+"-")
		}
	}
	// Also compare without provider/ prefix.
	if i := strings.LastIndex(bare, "/"); i >= 0 {
		bare = bare[i+1:]
	}
	for _, ex := range sa.ExcludedModels {
		if strings.EqualFold(modelID, ex) || strings.EqualFold(bare, ex) ||
			strings.HasSuffix(strings.ToLower(modelID), "/"+strings.ToLower(ex)) {
			return &Error{
				Message:    fmt.Sprintf("model_excluded: %s is excluded on this freebuff credential", modelID),
				HTTPStatus: 400,
				Code:       "model_excluded",
			}
		}
	}
	return nil
}

// FilterModelsByExcluded drops models listed in excluded_models for model.for_auth.
func FilterModelsByExcluded(models []Model, excluded []string) []Model {
	excluded = CleanStringList(excluded)
	if len(excluded) == 0 || len(models) == 0 {
		return models
	}
	ex := map[string]struct{}{}
	for _, e := range excluded {
		ex[strings.ToLower(e)] = struct{}{}
		if i := strings.LastIndex(e, "/"); i >= 0 {
			ex[strings.ToLower(e[i+1:])] = struct{}{}
		}
	}
	out := make([]Model, 0, len(models))
	for _, m := range models {
		id := strings.ToLower(m.ID)
		bare := id
		if i := strings.LastIndex(bare, "/"); i >= 0 {
			bare = bare[i+1:]
		}
		if _, ok := ex[id]; ok {
			continue
		}
		if _, ok := ex[bare]; ok {
			continue
		}
		out = append(out, m)
	}
	return out
}

func stringListFromAny(raw any) []string {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return CleanStringList(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return CleanStringList(out)
	case string:
		return CleanStringList(strings.Split(v, ","))
	default:
		return nil
	}
}

func modelAliasesFromAny(raw any) []ModelAlias {
	if raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var aliases []ModelAlias
	if json.Unmarshal(data, &aliases) != nil {
		return nil
	}
	return CleanModelAliases(aliases)
}

func anyToInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}
