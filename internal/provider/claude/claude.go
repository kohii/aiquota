// Package claude fetches Claude (claude.ai / Claude Code) subscription usage.
//
// Credentials are read once from the macOS Keychain item
// "Claude Code-credentials" via the `security` CLI (no Full Disk Access, no
// browser cookies). Usage comes from the OAuth usage endpoint.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kohii/aiquota/internal/httpx"
	"github.com/kohii/aiquota/internal/usage"
)

const (
	usageURL      = "https://api.anthropic.com/api/oauth/usage"
	keychainSvc   = "Claude Code-credentials"
	anthropicBeta = "oauth-2025-04-20"
	userAgent     = "claude-code/1.0.0"
	// securityBin is the macOS Keychain CLI. Use its absolute path so it resolves
	// regardless of the caller's PATH: launchers like Raycast pass a minimal PATH
	// that omits /usr/bin, which would otherwise make `security` "not found".
	securityBin = "/usr/bin/security"
)

// Provider implements usage.Provider for Claude.
type Provider struct {
	http    *httpx.Client
	account string // optional Keychain account; empty reads by service only
}

// New returns a Claude provider. account may be "" to match the Keychain item
// by service name alone.
func New(account string) *Provider {
	return &Provider{http: httpx.New(), account: account}
}

func (p *Provider) Name() string { return "claude" }

// Fetch reads the OAuth token from Keychain and queries the usage endpoint.
func (p *Provider) Fetch(ctx context.Context) (*usage.Usage, error) {
	cred, err := readCredential(ctx, p.account)
	if err != nil {
		return nil, err
	}
	body, err := p.http.Get(ctx, usageURL, map[string]string{
		"Authorization":  "Bearer " + cred.AccessToken,
		"anthropic-beta": anthropicBeta,
		"User-Agent":     userAgent,
	})
	if err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) && se.Unauthorized() {
			return nil, &usage.ReauthError{Provider: "claude", Hint: "`claude` で再ログインしてください"}
		}
		return nil, err
	}
	u, err := parseUsage(body)
	if err != nil {
		return nil, err
	}
	u.Plan = cred.SubscriptionType
	u.Source = "keychain:" + keychainSvc
	u.FetchedAt = time.Now()
	return u, nil
}

type credential struct {
	AccessToken      string
	SubscriptionType string
}

// readCredential pulls and parses the Keychain credential JSON.
func readCredential(ctx context.Context, account string) (*credential, error) {
	args := []string{"find-generic-password", "-s", keychainSvc}
	if account != "" {
		args = append(args, "-a", account)
	}
	args = append(args, "-w")
	out, err := exec.CommandContext(ctx, securityBin, args...).Output()
	if err != nil {
		// errSecItemNotFound (44): no such Keychain item -> claude not configured.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 {
			return nil, &usage.NotConfiguredError{Provider: "claude"}
		}
		return nil, fmt.Errorf("claude の Keychain 認証情報を読めません（`claude` でログイン済みですか）: %w", err)
	}
	var c struct {
		ClaudeAiOauth struct {
			AccessToken      string `json:"accessToken"`
			SubscriptionType string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &c); err != nil {
		return nil, fmt.Errorf("claude の Keychain 認証情報を解析できません: %w", err)
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return nil, errors.New("claude の Keychain 認証情報に accessToken がありません")
	}
	return &credential{
		AccessToken:      c.ClaudeAiOauth.AccessToken,
		SubscriptionType: c.ClaudeAiOauth.SubscriptionType,
	}, nil
}
