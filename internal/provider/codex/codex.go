// Package codex fetches OpenAI Codex / ChatGPT subscription usage.
//
// Credentials come from ~/.codex/auth.json (plaintext OAuth tokens written by
// the codex CLI; CODEX_HOME is honored). Usage comes from the wham endpoint.
// No Keychain, no browser cookies.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kohii/aiquota/internal/httpx"
	"github.com/kohii/aiquota/internal/usage"
)

const usageURL = "https://chatgpt.com/backend-api/wham/usage"

// Provider implements usage.Provider for Codex.
type Provider struct {
	http *httpx.Client
	home string // CODEX_HOME, defaults to ~/.codex
}

// New returns a Codex provider.
func New() *Provider {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".codex")
		}
	}
	return &Provider{http: httpx.New(), home: home}
}

func (p *Provider) Name() string { return "codex" }

// Fetch reads the local token and queries the wham usage endpoint.
func (p *Provider) Fetch(ctx context.Context) (*usage.Usage, error) {
	tok, accountID, err := loadAuth(filepath.Join(p.home, "auth.json"))
	if err != nil {
		return nil, err
	}
	body, err := p.http.Get(ctx, usageURL, map[string]string{
		"Authorization":      "Bearer " + tok,
		"ChatGPT-Account-Id": accountID,
		"User-Agent":         "aiquota/0.1",
	})
	if err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) && se.Unauthorized() {
			return nil, &usage.ReauthError{Provider: "codex", Hint: "`codex login` を実行してください"}
		}
		return nil, err
	}
	u, err := parseUsage(body)
	if err != nil {
		return nil, err
	}
	u.Source = "file:~/.codex/auth.json"
	u.FetchedAt = time.Now()
	return u, nil
}

// loadAuth extracts the access token and account id from auth.json. A missing
// file (codex not installed) or an empty token (never logged in) is reported as
// NotConfigured, not a hard error.
func loadAuth(path string) (token, accountID string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", &usage.NotConfiguredError{Provider: "codex"}
		}
		return "", "", fmt.Errorf("codex auth.json を読めません: %w", err)
	}
	var a struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", "", fmt.Errorf("codex auth.json を解析できません: %w", err)
	}
	if a.Tokens.AccessToken == "" {
		return "", "", &usage.NotConfiguredError{Provider: "codex", Reason: "ログインされていません（`codex login`）"}
	}
	return a.Tokens.AccessToken, a.Tokens.AccountID, nil
}
