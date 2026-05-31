package codex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kohii/aiquota/internal/usage"
)

func TestLoadAuth_MissingFile(t *testing.T) {
	// No auth.json -> codex not installed -> NotConfigured, not a hard error.
	_, _, err := loadAuth(filepath.Join(t.TempDir(), "auth.json"))
	var nc *usage.NotConfiguredError
	if !errors.As(err, &nc) {
		t.Errorf("err = %v, want NotConfiguredError", err)
	}
}

func TestLoadAuth_EmptyToken(t *testing.T) {
	// File present but no access_token -> logged out -> NotConfigured.
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"account_id":"acc"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := loadAuth(path)
	var nc *usage.NotConfiguredError
	if !errors.As(err, &nc) {
		t.Errorf("err = %v, want NotConfiguredError", err)
	}
}
