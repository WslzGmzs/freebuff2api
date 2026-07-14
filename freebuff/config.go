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

// AuthStorage is the on-disk freebuff.json shape aligned with CPA Auth fields
// plus Freebuff-specific storage.
//
// Host-standard fields (also accepted at top level of the auth file):
//
//	id, provider, prefix, label, disabled, proxy_url, priority,
//	attributes, metadata, status, unavailable, ...
//
// Freebuff-owned fields:
//
//	token / tokens, api_base_url, ad_providers, timezone, locale, os, debug,
//	login_mode (freebuff|codebuff), zeroclick_base_url, session_id, client_id
//
// See CPA sdk/cliproxy Auth and pluginapi.AuthData.
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
	if a.Priority != 0 {
		a.Metadata["priority"] = a.Priority
	}
	if a.Attributes == nil {
		a.Attributes = map[string]string{}
	}
	a.Attributes["provider"] = "freebuff"
	a.Attributes["login_mode"] = a.LoginMode
	a.Attributes["credential_source"] = src
	if a.Priority != 0 {
		a.Attributes["priority"] = strconv.Itoa(a.Priority)
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
