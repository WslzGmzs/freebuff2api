package freebuff

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	BaseFreebuff   = "https://freebuff.com"
	BaseCodebuff   = "https://www.codebuff.com"
	VerifyPath     = "/api/v1/freebuff/session"
	loginUserAgent = "Bun/1.3.11"
	LoginTTL       = 5 * time.Minute
	PollInterval   = 2 * time.Second
)

// LoginMode selects which host issues the CLI device code.
type LoginMode string

const (
	LoginModeFreebuff LoginMode = "freebuff"
	LoginModeCodebuff LoginMode = "codebuff"
)

// LoginEndpoints returns the CLI code/status URLs for a mode.
func LoginEndpoints(mode LoginMode) (codeURL, statusURL string) {
	base := BaseFreebuff
	if mode == LoginModeCodebuff {
		base = BaseCodebuff
	}
	return base + "/api/auth/cli/code", base + "/api/auth/cli/status"
}

// LoginSession holds an in-flight CLI device-code login.
type LoginSession struct {
	Mode            LoginMode
	FingerprintID   string
	FingerprintHash string
	ExpiresAt       int64
	StatusURL       string
	LoginURL        string
	CreatedAt       time.Time
}

// LoginUser is returned when polling succeeds.
type LoginUser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AuthToken string `json:"authToken"`
}

// VerifyResult is the outcome of probing a token against Freebuff session API.
type VerifyResult struct {
	OK   bool   `json:"ok"`
	Info string `json:"info"`
}

func loginHTTPClient(proxyURL string) *http.Client {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   20 * time.Second,
			KeepAlive: 20 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        8,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}
}

// StartCLILogin requests a device code and login URL (tool/get_token.py flow).
func StartCLILogin(ctx context.Context, mode LoginMode, proxyURL string) (*LoginSession, error) {
	if mode == "" {
		mode = LoginModeFreebuff
	}
	if mode != LoginModeFreebuff && mode != LoginModeCodebuff {
		return nil, fmt.Errorf("unsupported login mode: %s", mode)
	}
	codeURL, statusURL := LoginEndpoints(mode)
	fingerprintID := "fb-" + randomHex(8)

	body, _ := json.Marshal(map[string]string{"fingerprintId": fingerprintID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codeURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", loginUserAgent)

	resp, err := loginHTTPClient(proxyURL).Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth code request failed: %w", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("auth code HTTP %d: %s", resp.StatusCode, truncate(string(payload), 200))
	}
	var data struct {
		LoginURL        string `json:"loginUrl"`
		FingerprintHash string `json:"fingerprintHash"`
		ExpiresAt       int64  `json:"expiresAt"`
	}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, fmt.Errorf("auth code invalid JSON: %w", err)
	}
	if data.LoginURL == "" || data.FingerprintHash == "" {
		return nil, fmt.Errorf("auth code missing loginUrl or fingerprintHash")
	}
	return &LoginSession{
		Mode:            mode,
		FingerprintID:   fingerprintID,
		FingerprintHash: data.FingerprintHash,
		ExpiresAt:       data.ExpiresAt,
		StatusURL:       statusURL,
		LoginURL:        data.LoginURL,
		CreatedAt:       time.Now(),
	}, nil
}

// PollCLILogin once-shot polls the CLI status endpoint (host drives cadence).
// Returns (user, pending, error). pending=true means keep waiting.
func PollCLILogin(ctx context.Context, session *LoginSession, proxyURL string) (*LoginUser, bool, error) {
	if session == nil {
		return nil, false, fmt.Errorf("nil login session")
	}
	if time.Since(session.CreatedAt) > LoginTTL {
		return nil, false, fmt.Errorf("login expired")
	}
	qs := url.Values{
		"fingerprintId":   {session.FingerprintID},
		"fingerprintHash": {session.FingerprintHash},
		"expiresAt":       {fmt.Sprintf("%d", session.ExpiresAt)},
	}
	fullURL := session.StatusURL + "?" + qs.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", loginUserAgent)

	resp, err := loginHTTPClient(proxyURL).Do(req)
	if err != nil {
		// Transient network: treat as pending so host keeps polling.
		return nil, true, nil
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, true, nil
	}
	if resp.StatusCode >= 400 {
		return nil, false, fmt.Errorf("status HTTP %d: %s", resp.StatusCode, truncate(string(payload), 200))
	}
	var data struct {
		User *LoginUser `json:"user"`
	}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, true, nil
	}
	if data.User == nil || strings.TrimSpace(data.User.AuthToken) == "" {
		return nil, true, nil
	}
	return data.User, false, nil
}

// VerifyToken checks a bearer token against Codebuff Freebuff session API.
func VerifyToken(ctx context.Context, token, proxyURL string) VerifyResult {
	token = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(token, "Bearer "), "bearer "))
	if token == "" {
		return VerifyResult{OK: false, Info: "missing token"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseCodebuff+VerifyPath, nil)
	if err != nil {
		return VerifyResult{OK: false, Info: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", loginUserAgent)

	resp, err := loginHTTPClient(proxyURL).Do(req)
	if err != nil {
		return VerifyResult{OK: false, Info: fmt.Sprintf("network error: %v", err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return VerifyResult{OK: false, Info: fmt.Sprintf("HTTP %d (token rejected): %s", resp.StatusCode, truncate(string(body), 200))}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return VerifyResult{OK: true, Info: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	// Other 4xx/5xx after auth often still mean the token was accepted.
	return VerifyResult{OK: true, Info: fmt.Sprintf("HTTP %d (auth ok, endpoint returned: %s)", resp.StatusCode, truncate(string(body), 200))}
}

// AuthStorageFromToken builds a freebuff.json payload after successful login.
func AuthStorageFromToken(token string, user *LoginUser, mode LoginMode) AuthStorage {
	label := "Freebuff"
	if user != nil {
		if user.Name != "" {
			label = user.Name
		} else if user.Email != "" {
			label = user.Email
		}
	}
	if mode == LoginModeCodebuff {
		label = label + " (codebuff)"
	}
	return AuthStorage{
		Token: token,
		Label: label,
	}
}
