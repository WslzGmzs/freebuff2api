package freebuff

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
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

// AuthStorage is the persisted CPA credential file shape (freebuff.json).
//
//	{
//	  "token": "single-or-comma-separated",
//	  "tokens": ["a","b"],
//	  "api_base_url": "https://www.codebuff.com",
//	  "ad_providers": ["gravity","zeroclick"],
//	  "proxy_url": "",
//	  "timezone": "Asia/Shanghai",
//	  "locale": "zh-CN",
//	  "os": "windows"
//	}
type AuthStorage struct {
	Token       string   `json:"token,omitempty"`
	Tokens      []string `json:"tokens,omitempty"`
	APIBaseURL  string   `json:"api_base_url,omitempty"`
	ZeroClick   string   `json:"zeroclick_base_url,omitempty"`
	AdProviders []string `json:"ad_providers,omitempty"`
	ProxyURL    string   `json:"proxy_url,omitempty"`
	Timezone    string   `json:"timezone,omitempty"`
	Locale      string   `json:"locale,omitempty"`
	OS          string   `json:"os,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	ClientID    string   `json:"client_id,omitempty"`
	Debug       bool     `json:"debug,omitempty"`
	// Label is optional display text in the CPA UI.
	Label string `json:"label,omitempty"`
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
	return out
}

// ParseAuthStorage decodes freebuff credential JSON.
func ParseAuthStorage(raw []byte) (AuthStorage, error) {
	var a AuthStorage
	if err := json.Unmarshal(raw, &a); err != nil {
		return AuthStorage{}, err
	}
	// Also accept {"access_token":"..."} / plain string edge cases via loose map.
	if len(a.TokenList()) == 0 {
		var loose map[string]any
		if err := json.Unmarshal(raw, &loose); err == nil {
			for _, key := range []string{"access_token", "accessToken", "FREEBUFF_TOKEN", "codebuff_token"} {
				if v, ok := loose[key].(string); ok && strings.TrimSpace(v) != "" {
					a.Token = v
					break
				}
			}
		}
	}
	return a, nil
}

// ToSettings builds client settings for a single token.
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
