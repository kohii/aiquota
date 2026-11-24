package claude

import (
	"errors"
	"testing"

	"github.com/kohii/aiquota/internal/usage"
)

func TestParseCredential(t *testing.T) {
	c, err := parseCredential([]byte(`{"claudeAiOauth":{"accessToken":"tok","subscriptionType":"team"}}` + "\n"))
	if err != nil {
		t.Fatalf("parseCredential: %v", err)
	}
	if c.AccessToken != "tok" || c.SubscriptionType != "team" {
		t.Errorf("got %+v", c)
	}
}

// Claude Code leaves this shape behind after its refresh token is rejected.
func TestParseCredential_DeadLoginNeedsReauth(t *testing.T) {
	raw := []byte(`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0,"subscriptionType":"team"}}`)
	_, err := parseCredential(raw)
	var re *usage.ReauthError
	if !errors.As(err, &re) {
		t.Fatalf("want ReauthError, got %v", err)
	}
}

func TestClaudeCodeAccount(t *testing.T) {
	cases := []struct{ envUser, username, want string }{
		{"kohei", "other", "kohei"},
		{"", "kohei", "kohei"},
		{"", "", "claude-code-user"},
		{"has space", "kohei", "claude-code-user"},
	}
	for _, c := range cases {
		if got := claudeCodeAccount(c.envUser, c.username); got != c.want {
			t.Errorf("claudeCodeAccount(%q, %q) = %q, want %q", c.envUser, c.username, got, c.want)
		}
	}
}
