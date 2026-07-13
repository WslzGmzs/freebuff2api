package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/WslzGmzs/freebuff2api/freebuff"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// managementRegistration declares CPA management routes + browser resource UI.
func managementRegistration() map[string]any {
	return map[string]any{
		"resources": []map[string]any{
			{
				"Path":        "/",
				"Menu":        "Freebuff Token",
				"Description": "CLI 扫码登录获取 Freebuff Bearer token（原 tool/web 能力）。",
			},
			{
				"Path":        "/token",
				"Menu":        "Freebuff Token",
				"Description": "Alias for the Freebuff token helper page.",
			},
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
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		// Loose decode for hosts that use different field casing.
		var loose map[string]any
		if err2 := json.Unmarshal(raw, &loose); err2 != nil {
			return nil, err
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
		case []any:
			// ignore
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
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	p := normalizeMgmtPath(req.Path)

	// Resource HTML (unauthenticated under /v0/resource/plugins/freebuff/...).
	if method == http.MethodGet && (p == "/" || p == "" || p == "/token" || p == "/index.html") {
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "Cache-Control": []string{"no-store"}},
			Body:       []byte(tokenHelperHTML),
		})
	}

	switch {
	case method == http.MethodPost && strings.HasSuffix(p, "/login/start"):
		return d.mgmtLoginStart(req.Body)
	case method == http.MethodPost && strings.HasSuffix(p, "/login/poll"):
		return d.mgmtLoginPoll(req.Body)
	case method == http.MethodPost && strings.HasSuffix(p, "/login/verify"):
		return d.mgmtLoginVerify(req.Body)
	case method == http.MethodGet && strings.HasSuffix(p, "/status"):
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body: mustJSON(map[string]any{
				"plugin":  ProviderName,
				"version": PluginVer,
				"ok":      true,
			}),
		})
	default:
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusNotFound,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       mustJSON(map[string]any{"error": "not found", "path": p, "method": method}),
		})
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
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusBadGateway,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       mustJSON(map[string]any{"error": err.Error()}),
		})
	}
	d.loginSessions.Store(session.FingerprintID, session)
	return OkEnvelope(pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body: mustJSON(map[string]any{
			"mode":       string(session.Mode),
			"state":      session.FingerprintID,
			"login_url":  session.LoginURL,
			"expires_at": session.ExpiresAt,
		}),
	})
}

func (d *Dispatcher) mgmtLoginPoll(body []byte) ([]byte, error) {
	var in struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(body, &in)
	state := strings.TrimSpace(in.State)
	if state == "" {
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusBadRequest,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       mustJSON(map[string]any{"status": "error", "message": "missing state"}),
		})
	}
	v, ok := d.loginSessions.Load(state)
	if !ok {
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       mustJSON(map[string]any{"status": "error", "message": "unknown state (restart login)"}),
		})
	}
	session := v.(*freebuff.LoginSession)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user, pending, err := freebuff.PollCLILogin(ctx, session, "")
	if err != nil {
		d.loginSessions.Delete(state)
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       mustJSON(map[string]any{"status": "error", "message": err.Error()}),
		})
	}
	if pending {
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       mustJSON(map[string]any{"status": "pending", "message": "waiting for browser login"}),
		})
	}
	vr := freebuff.VerifyToken(ctx, user.AuthToken, "")
	if !vr.OK {
		d.loginSessions.Delete(state)
		return OkEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       mustJSON(map[string]any{"status": "error", "message": "token rejected: " + vr.Info}),
		})
	}
	d.loginSessions.Delete(state)
	return OkEnvelope(pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body: mustJSON(map[string]any{
			"status": "success",
			"token":  user.AuthToken,
			"id":     user.ID,
			"name":   user.Name,
			"email":  user.Email,
			"mode":   string(session.Mode),
			"auth": map[string]any{
				"token": user.AuthToken,
			},
		}),
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
	return OkEnvelope(pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       mustJSON(map[string]any{"ok": vr.OK, "info": vr.Info}),
	})
}

func normalizeMgmtPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	// Accept full CPA paths or relative plugin paths.
	p = strings.ReplaceAll(p, "\\", "/")
	// Strip known prefixes.
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
	// Clean but keep trailing semantics for root.
	cleaned := path.Clean(p)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}
