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
	"os"
	"os/exec"
	"os/user"
	"regexp"
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
	account string // Keychain account the credential item is filed under
}

// New returns a Claude provider. account may be "" to use the account Claude
// Code itself files its credentials under.
func New(account string) *Provider {
	if account == "" {
		username := ""
		if u, err := user.Current(); err == nil {
			username = u.Username
		}
		account = claudeCodeAccount(os.Getenv("USER"), username)
	}
	return &Provider{http: httpx.New(), account: account}
}

var safeAccount = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// claudeCodeAccount mirrors how Claude Code names the Keychain account it
// reads and writes. The service name alone is not enough: stale items under
// other accounts can share it, and `security` would return whichever it finds
// first.
func claudeCodeAccount(envUser, username string) string {
	name := envUser
	if name == "" {
		name = username
	}
	if !safeAccount.MatchString(name) {
		return "claude-code-user"
	}
	return name
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
	out, err := exec.CommandContext(ctx, securityBin,
		"find-generic-password", "-s", keychainSvc, "-a", account, "-w").Output()
	if err != nil {
		// errSecItemNotFound (44): no such Keychain item -> claude not configured.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 {
			return nil, &usage.NotConfiguredError{Provider: "claude"}
		}
		return nil, fmt.Errorf("claude の Keychain 認証情報を読めません（`claude` でログイン済みですか）: %w", err)
	}
	return parseCredential(out)
}

// parseCredential decodes the Keychain credential JSON.
func parseCredential(raw []byte) (*credential, error) {
	var c struct {
		ClaudeAiOauth struct {
			AccessToken      string `json:"accessToken"`
			SubscriptionType string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &c); err != nil {
		return nil, fmt.Errorf("claude の Keychain 認証情報を解析できません: %w", err)
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		// Claude Code blanks the tokens (keeping the rest of claudeAiOauth) when
		// the server rejects its refresh token, so an empty token means the
		// stored login is dead rather than malformed.
		return nil, &usage.ReauthError{Provider: "claude", Hint: "`claude` で /login し直してください"}
	}
	return &credential{
		AccessToken:      c.ClaudeAiOauth.AccessToken,
		SubscriptionType: c.ClaudeAiOauth.SubscriptionType,
	}, nil
}
