package copilot

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kohii/aiquota/internal/usage"
)

// writeApps writes an apps.json fixture and returns its path.
func writeApps(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "apps.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadToken_PrefersGitHubDotCom(t *testing.T) {
	// An enterprise entry and the github.com entry coexist; github.com wins
	// regardless of map iteration order.
	path := writeApps(t, `{
		"corp.ghe.com:Iv1.aaa": {"oauth_token": "ghu_enterprise", "user": "kohii"},
		"github.com:Iv1.b507a08c87ecfe98": {"oauth_token": "ghu_dotcom", "user": "kohii"}
	}`)
	tok, err := loadToken(path)
	if err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if tok != "ghu_dotcom" {
		t.Errorf("token = %q, want github.com token", tok)
	}
}

func TestLoadToken_FallsBackToAnyHost(t *testing.T) {
	// No github.com entry: any host with a token is used.
	path := writeApps(t, `{"corp.ghe.com:Iv1.aaa": {"oauth_token": "ghu_enterprise"}}`)
	tok, err := loadToken(path)
	if err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if tok != "ghu_enterprise" {
		t.Errorf("token = %q, want enterprise token", tok)
	}
}

func TestLoadToken_NoToken(t *testing.T) {
	// File present but no token -> logged out -> NotConfigured (not a hard error).
	path := writeApps(t, `{"github.com:Iv1.aaa": {"user": "kohii"}}`)
	_, err := loadToken(path)
	var nc *usage.NotConfiguredError
	if !errors.As(err, &nc) {
		t.Errorf("err = %v, want NotConfiguredError", err)
	}
}

func TestLoadToken_MissingFile(t *testing.T) {
	// Missing file -> Copilot not installed -> NotConfigured.
	_, err := loadToken(filepath.Join(t.TempDir(), "absent.json"))
	var nc *usage.NotConfiguredError
	if !errors.As(err, &nc) {
		t.Errorf("err = %v, want NotConfiguredError", err)
	}
}
