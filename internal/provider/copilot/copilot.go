// Package copilot fetches GitHub Copilot subscription usage.
//
// Credentials come from the GitHub Copilot editor plugins' plaintext token
// store ~/.config/github-copilot/apps.json (XDG_CONFIG_HOME honored), the same
// file VS Code / Neovim / the Copilot CLI write after login. No Keychain, no
// Full Disk Access, no browser cookies. Usage comes from the copilot_internal
// endpoint on api.github.com.
package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kohii/aiquota/internal/httpx"
	"github.com/kohii/aiquota/internal/usage"
)

const usageURL = "https://api.github.com/copilot_internal/user"

// Editor identification headers. GitHub gates copilot_internal behind an
// editor-looking client, so we present the Copilot Chat plugin identity (the
// same values the official plugin sends). The token itself is the only secret.
const (
	editorVersion = "vscode/1.96.2"
	pluginVersion = "copilot-chat/0.26.7"
	userAgent     = "GitHubCopilotChat/0.26.7"
	apiVersion    = "2025-04-01"
)

// Provider implements usage.Provider for GitHub Copilot.
type Provider struct {
	http *httpx.Client
	path string // apps.json path
}

// New returns a Copilot provider reading the default apps.json location.
func New() *Provider {
	return &Provider{http: httpx.New(), path: defaultAppsPath()}
}

func (p *Provider) Name() string { return "copilot" }

// Fetch reads the local OAuth token and queries the copilot_internal endpoint.
func (p *Provider) Fetch(ctx context.Context) (*usage.Usage, error) {
	tok, err := loadToken(p.path)
	if err != nil {
		return nil, err
	}
	body, err := p.http.Get(ctx, usageURL, map[string]string{
		// GitHub's copilot_internal API uses the "token" scheme, not Bearer.
		"Authorization":         "token " + tok,
		"Accept":                "application/json",
		"Editor-Version":        editorVersion,
		"Editor-Plugin-Version": pluginVersion,
		"User-Agent":            userAgent,
		"X-Github-Api-Version":  apiVersion,
	})
	if err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) && se.Unauthorized() {
			return nil, &usage.ReauthError{Provider: "copilot", Hint: "VS Code 等で GitHub Copilot に再ログインしてください"}
		}
		return nil, err
	}
	u, err := parseUsage(body)
	if err != nil {
		return nil, err
	}
	u.Source = "file:~/.config/github-copilot/apps.json"
	u.FetchedAt = time.Now()
	return u, nil
}

// defaultAppsPath returns $XDG_CONFIG_HOME/github-copilot/apps.json, falling
// back to ~/.config/github-copilot/apps.json.
func defaultAppsPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "github-copilot", "apps.json")
}

// loadToken extracts the github.com OAuth token from apps.json. The file maps
// "<host>:<appId>" keys to {oauth_token, user, ...}; we prefer the github.com
// host and fall back to any entry with a token (e.g. a single enterprise host).
func loadToken(path string) (string, error) {
	if path == "" {
		return "", errors.New("github copilot の apps.json パスを特定できません")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &usage.NotConfiguredError{Provider: "copilot"}
		}
		return "", fmt.Errorf("github copilot の apps.json を読めません: %w", err)
	}
	var m map[string]struct {
		OAuthToken string `json:"oauth_token"`
		User       string `json:"user"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("github copilot の apps.json を解析できません: %w", err)
	}

	var fallback string
	for key, v := range m {
		if v.OAuthToken == "" {
			continue
		}
		host := key
		if i := strings.IndexByte(key, ':'); i >= 0 {
			host = key[:i]
		}
		if host == "github.com" {
			return v.OAuthToken, nil
		}
		if fallback == "" {
			fallback = v.OAuthToken
		}
	}
	if fallback == "" {
		return "", &usage.NotConfiguredError{Provider: "copilot", Reason: "ログインされていません（VS Code 等で Copilot にログイン）"}
	}
	return fallback, nil
}
