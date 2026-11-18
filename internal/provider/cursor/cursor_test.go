package cursor

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kohii/aiquota/internal/usage"
)

func TestResolve_NoDBNoCLI(t *testing.T) {
	called := false
	p := &Provider{
		dbPath:   filepath.Join(t.TempDir(), "state.vscdb"),
		cliPaths: []string{filepath.Join(t.TempDir(), "cursor-agent")},
		keychain: func(context.Context) (string, error) {
			called = true
			return "", errors.New("keychain should not be called")
		},
	}
	_, err := p.resolve(context.Background())
	var nc *usage.NotConfiguredError
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v, want NotConfiguredError", err)
	}
	if nc.Reason != "" {
		t.Errorf("reason = %q, want empty", nc.Reason)
	}
	if called {
		t.Fatal("keychain was called")
	}
}

func TestResolve_IDELoggedOutNoCLI(t *testing.T) {
	called := false
	p := &Provider{
		dbPath:   writeEmptyVSCDB(t),
		cliPaths: []string{filepath.Join(t.TempDir(), "cursor-agent")},
		keychain: func(context.Context) (string, error) {
			called = true
			return "", errors.New("keychain should not be called")
		},
	}
	_, err := p.resolve(context.Background())
	var nc *usage.NotConfiguredError
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v, want NotConfiguredError", err)
	}
	if nc.Reason != ideLoggedOut {
		t.Errorf("reason = %q, want %q", nc.Reason, ideLoggedOut)
	}
	if called {
		t.Fatal("keychain was called")
	}
}

func TestResolve_EmptyVSCDBUsesKeychain(t *testing.T) {
	const tok = "eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1c2VyXzEifQ.sig"
	p := &Provider{
		dbPath:   writeEmptyVSCDB(t),
		cliPaths: []string{writeCursorAgent(t)},
		keychain: func(context.Context) (string, error) {
			return tok, nil
		},
	}
	sess, err := p.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sess.token != tok || sess.source != sourceKeychain {
		t.Errorf("session = %+v", sess)
	}
}

func TestResolve_KeychainToken(t *testing.T) {
	const tok = "eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1c2VyXzEifQ.sig"
	p := &Provider{
		dbPath:   filepath.Join(t.TempDir(), "state.vscdb"),
		cliPaths: []string{writeCursorAgent(t)},
		keychain: func(context.Context) (string, error) {
			return tok, nil
		},
	}
	sess, err := p.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sess.token != tok {
		t.Errorf("token = %q, want %q", sess.token, tok)
	}
	if sess.source != "keychain:cursor-access-token" {
		t.Errorf("source = %q, want keychain:cursor-access-token", sess.source)
	}
}

func TestResolve_KeychainNotConfigured(t *testing.T) {
	p := &Provider{
		dbPath:   filepath.Join(t.TempDir(), "state.vscdb"),
		cliPaths: []string{writeCursorAgent(t)},
		keychain: func(context.Context) (string, error) {
			return "", &usage.NotConfiguredError{Provider: "cursor"}
		},
	}
	_, err := p.resolve(context.Background())
	var nc *usage.NotConfiguredError
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v, want NotConfiguredError", err)
	}
}

func TestResolve_VSCDBSkipsKeychain(t *testing.T) {
	const tok = "db-token"
	called := false
	p := &Provider{
		dbPath:   writeVSCDB(t, tok),
		cliPaths: []string{writeCursorAgent(t)},
		keychain: func(context.Context) (string, error) {
			called = true
			return "keychain-token", nil
		},
	}
	sess, err := p.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sess.token != tok {
		t.Errorf("token = %q, want %q", sess.token, tok)
	}
	if sess.source != "vscdb:state.vscdb" {
		t.Errorf("source = %q, want vscdb:state.vscdb", sess.source)
	}
	if called {
		t.Fatal("keychain was called")
	}
}

func writeCursorAgent(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cursor-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeEmptyVSCDB(t *testing.T) string {
	t.Helper()
	return writeVSCDB(t, "")
}

func writeVSCDB(t *testing.T, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if token != "" {
		if _, err := db.Exec(`INSERT INTO ItemTable (key, value) VALUES (?, ?)`, tokenKey, token); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
