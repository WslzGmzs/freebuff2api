package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/WslzGmzs/freebuff2api/freebuff"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// managementRegistration declares CPA management routes + browser resource UI.
//
// Login/verify for the browser page use **resource** paths under
// /v0/resource/plugins/freebuff/... (no management key). Authenticated
// /v0/management/... routes remain available for host UIs that already send a key.
func managementRegistration() map[string]any {
	return map[string]any{
		"resources": []map[string]any{
			{
				"Path":        "/",
				"Menu":        "Freebuff Token",
				"Description": "CLI 扫码登录获取 Freebuff Bearer token（无需 management key）。",
			},
			{
				"Path":        "/token",
				"Menu":        "Freebuff Token",
				"Description": "Alias for the Freebuff token helper page.",
			},
			// Unauthenticated JSON API for the resource page (GET + query).
			{"Path": "/api/start", "Description": "Start CLI device-code login (?mode=freebuff|codebuff)."},
			{"Path": "/api/poll", "Description": "Poll login status (?state=fingerprint_id)."},
			{"Path": "/api/verify", "Description": "Verify a bearer token (?token=...)."},
			{"Path": "/api/status", "Description": "Plugin status JSON."},
		},
		"routes": []map[string]any{
			{"Method": "POST", "Path": "/plugins/freebuff/login/start"},
			{"Method": "POST", "Path": "/plugins/freebuff/login/poll"},
			{"Method": "POST", "Path": "/plugins/freebuff/login/verify"},
			{"Method": "GET", "Path": "/plugins/freebuff/status"},
		},
	}
}

// HandleManagement serves management API routes and resource pages.
func (d *Dispatcher) HandleManagement(raw []byte) ([]byte, error) {
	req, err := decodeManagementRequest(raw)
	if err != nil {
		return nil, err
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	p := normalizeMgmtPath(req.Path)
	q := req.Query
	if q == nil {
		q = url.Values{}
	}
	// Some hosts put the raw query only in Path.
	if len(q) == 0 {
		if u, err := url.Parse(req.Path); err == nil && u.RawQuery != "" {
			q = u.Query()
			p = normalizeMgmtPath(u.Path)
		}
	}

	// Resource HTML (unauthenticated).
	if method == http.MethodGet && (p == "/" || p == "" || p == "/token" || p == "/index.html") {
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "Cache-Control": []string{"no-store"}},
			Body:       []byte(tokenHelperHTML),
		})
	}

	// Resource JSON API — no management key (same host as HTML page).
	if method == http.MethodGet && strings.HasPrefix(p, "/api/") {
		return d.handleResourceAPI(p, q)
	}

	// Authenticated management routes (optional; for tooling that has a key).
	switch {
	case method == http.MethodPost && strings.HasSuffix(p, "/login/start"):
		return d.mgmtLoginStart(req.Body)
	case method == http.MethodPost && strings.HasSuffix(p, "/login/poll"):
		return d.mgmtLoginPoll(req.Body)
	case method == http.MethodPost && strings.HasSuffix(p, "/login/verify"):
		return d.mgmtLoginVerify(req.Body)
	case method == http.MethodGet && strings.HasSuffix(p, "/status"):
		return jsonOK(map[string]any{
			"plugin":  ProviderName,
			"version": PluginVer,
			"ok":      true,
		})
	default:
		return jsonStatus(http.StatusNotFound, map[string]any{"error": "not found", "path": p, "method": method})
	}
}

func (d *Dispatcher) handleResourceAPI(p string, q url.Values) ([]byte, error) {
	switch p {
	case "/api/start":
		mode := q.Get("mode")
		if mode == "" {
			mode = "freebuff"
		}
		body, _ := json.Marshal(map[string]string{"mode": mode})
		return d.mgmtLoginStart(body)
	case "/api/poll":
		state := q.Get("state")
		body, _ := json.Marshal(map[string]string{"state": state})
		return d.mgmtLoginPoll(body)
	case "/api/verify":
		token := q.Get("token")
		body, _ := json.Marshal(map[string]string{"token": token})
		return d.mgmtLoginVerify(body)
	case "/api/status":
		return jsonOK(map[string]any{
			"plugin":  ProviderName,
			"version": PluginVer,
			"ok":      true,
		})
	default:
		return jsonStatus(http.StatusNotFound, map[string]any{"error": "not found", "path": p})
	}
}

func (d *Dispatcher) mgmtLoginStart(body []byte) ([]byte, error) {
	mode := freebuff.LoginModeFreebuff
	var in struct {
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(body, &in)
	if in.Mode != "" {
		mode = freebuff.LoginMode(in.Mode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := freebuff.StartCLILogin(ctx, mode, "")
	if err != nil {
		return jsonStatus(http.StatusBadGateway, map[string]any{"error": err.Error()})
	}
	d.loginSessions.Store(session.FingerprintID, session)
	return jsonOK(map[string]any{
		"mode":       string(session.Mode),
		"state":      session.FingerprintID,
		"login_url":  session.LoginURL,
		"expires_at": session.ExpiresAt,
	})
}

func (d *Dispatcher) mgmtLoginPoll(body []byte) ([]byte, error) {
	var in struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(body, &in)
	state := strings.TrimSpace(in.State)
	if state == "" {
		return jsonOK(map[string]any{"status": "error", "message": "missing state"})
	}
	v, ok := d.loginSessions.Load(state)
	if !ok {
		return jsonOK(map[string]any{"status": "error", "message": "unknown state (restart login)"})
	}
	session := v.(*freebuff.LoginSession)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user, pending, err := freebuff.PollCLILogin(ctx, session, "")
	if err != nil {
		d.loginSessions.Delete(state)
		return jsonOK(map[string]any{"status": "error", "message": err.Error()})
	}
	if pending {
		return jsonOK(map[string]any{"status": "pending", "message": "waiting for browser login"})
	}
	vr := freebuff.VerifyToken(ctx, user.AuthToken, "")
	if !vr.OK {
		d.loginSessions.Delete(state)
		return jsonOK(map[string]any{"status": "error", "message": "token rejected: " + vr.Info})
	}
	d.loginSessions.Delete(state)
	return jsonOK(map[string]any{
		"status": "success",
		"token":  user.AuthToken,
		"id":     user.ID,
		"name":   user.Name,
		"email":  user.Email,
		"mode":   string(session.Mode),
		"auth": map[string]any{
			"token": user.AuthToken,
		},
	})
}

func (d *Dispatcher) mgmtLoginVerify(body []byte) ([]byte, error) {
	var in struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &in)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	vr := freebuff.VerifyToken(ctx, in.Token, "")
	return jsonOK(map[string]any{"ok": vr.OK, "info": vr.Info})
}

func decodeManagementRequest(raw []byte) (pluginapi.ManagementRequest, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err == nil && (req.Path != "" || req.Method != "") {
		return req, nil
	}
	var loose map[string]any
	if err := json.Unmarshal(raw, &loose); err != nil {
		return req, err
	}
	req.Method, _ = loose["Method"].(string)
	if req.Method == "" {
		req.Method, _ = loose["method"].(string)
	}
	req.Path, _ = loose["Path"].(string)
	if req.Path == "" {
		req.Path, _ = loose["path"].(string)
	}
	switch b := loose["Body"].(type) {
	case string:
		req.Body = []byte(b)
	case []byte:
		req.Body = b
	default:
		if rawBody, ok := loose["body"]; ok {
			switch t := rawBody.(type) {
			case string:
				req.Body = []byte(t)
			case []byte:
				req.Body = t
			}
		}
	}
	// Query may arrive as map[string][]string or map[string]any
	if qraw, ok := loose["Query"]; ok {
		req.Query = coerceQuery(qraw)
	} else if qraw, ok := loose["query"]; ok {
		req.Query = coerceQuery(qraw)
	}
	return req, nil
}

func coerceQuery(v any) url.Values {
	out := url.Values{}
	switch t := v.(type) {
	case url.Values:
		return t
	case map[string][]string:
		for k, vals := range t {
			for _, val := range vals {
				out.Add(k, val)
			}
		}
	case map[string]any:
		for k, val := range t {
			switch x := val.(type) {
			case string:
				out.Set(k, x)
			case []any:
				for _, item := range x {
					if s, ok := item.(string); ok {
						out.Add(k, s)
					}
				}
			case []string:
				for _, s := range x {
					out.Add(k, s)
				}
			}
		}
	}
	return out
}

func normalizeMgmtPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	p = strings.ReplaceAll(p, "\\", "/")
	// Drop query string if still attached.
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	for _, prefix := range []string{
		"/v0/resource/plugins/freebuff",
		"/v0/management/plugins/freebuff",
		"/v0/management",
		"/plugins/freebuff",
	} {
		if strings.HasPrefix(p, prefix) {
			p = strings.TrimPrefix(p, prefix)
			break
		}
	}
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	cleaned := path.Clean(p)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func jsonOK(v any) ([]byte, error) {
	return jsonStatus(http.StatusOK, v)
}

func jsonStatus(code int, v any) ([]byte, error) {
	return OkEnvelope(pluginapi.ManagementResponse{
		StatusCode: code,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}, "Cache-Control": []string{"no-store"}},
		Body:       mustJSON(v),
	})
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}
