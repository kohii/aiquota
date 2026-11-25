package cursor

import (
	"context"
	"encoding/base64"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// Produced outside this package: PBKDF2-HMAC-SHA1 via Python hashlib, then
// OpenSSL AES-128-CBC with a fixed IV of 16 0x20 bytes.
const (
	safeStorageTestPassword = "test-password"
	safeStorageTestJWT      = "x.eyJzdWIiOiJnaXRodWJ8MTIzIn0.y"
	safeStorageTestBlobB64  = "djEwXWiWo5RRQv5YbT9McdIYLRVi8Da26xgdk/ytY72b9ps="
	safeStorageNotJWTBlob   = "djEw5cZsfOeJBiklB8jv9xpoug=="
)

func TestDecryptSafeStorage_FixedVector(t *testing.T) {
	blob, err := base64.StdEncoding.DecodeString(safeStorageTestBlobB64)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptSafeStorage(blob, safeStorageTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if plain != safeStorageTestJWT {
		t.Fatalf("plain = %q", plain)
	}
	sub, err := jwtSubject(plain)
	if err != nil {
		t.Fatal(err)
	}
	if sub != "github|123" {
		t.Fatalf("sub = %q", sub)
	}
}

func TestDecryptSafeStorage_WrongPassword(t *testing.T) {
	blob, err := base64.StdEncoding.DecodeString(safeStorageTestBlobB64)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptSafeStorage(blob, "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if plain == safeStorageTestJWT {
		t.Fatal("wrong password returned the plaintext")
	}
}

func TestResolve_EncryptedToken(t *testing.T) {
	stored := safeStoragePrefix + safeStorageTestBlobB64
	cliCalled := false
	p := &Provider{
		dbPath:   writeVSCDB(t, stored),
		cliPaths: []string{writeCursorAgent(t)},
		keychain: func(context.Context) (string, error) {
			cliCalled = true
			return "cli-token", nil
		},
		safeStoragePassword: func(context.Context, string, string) (string, error) {
			return safeStorageTestPassword, nil
		},
	}
	sess, err := p.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sess.token != safeStorageTestJWT || sess.source != sourceVSCDB {
		t.Fatalf("session = %+v", sess)
	}
	if cliCalled {
		t.Fatal("cursor-access-token was read")
	}
}

func TestResolve_EncryptedOnLinuxWithReader(t *testing.T) {
	p := &Provider{
		dbPath: writeVSCDB(t, safeStoragePrefix+safeStorageTestBlobB64),
		goos:   "linux",
		safeStoragePassword: func(context.Context, string, string) (string, error) {
			return safeStorageTestPassword, nil
		},
	}
	sess, err := p.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sess.token != safeStorageTestJWT {
		t.Fatalf("token = %q", sess.token)
	}
}

func TestResolve_EncryptedNonDarwin(t *testing.T) {
	cliCalled := false
	p := &Provider{
		dbPath:   writeVSCDB(t, safeStoragePrefix+safeStorageTestBlobB64),
		cliPaths: []string{writeCursorAgent(t)},
		goos:     "linux",
		keychain: func(context.Context) (string, error) {
			cliCalled = true
			return "cli-token", nil
		},
	}
	_, err := p.resolve(context.Background())
	if err == nil || err.Error() != errSafeStorageOS {
		t.Fatalf("err = %v", err)
	}
	if cliCalled {
		t.Fatal("cursor-access-token was read")
	}
}

func TestResolve_EncryptedNotJWT(t *testing.T) {
	p := &Provider{
		dbPath: writeVSCDB(t, safeStoragePrefix+safeStorageNotJWTBlob),
		safeStoragePassword: func(context.Context, string, string) (string, error) {
			return safeStorageTestPassword, nil
		},
	}
	_, err := p.resolve(context.Background())
	if err == nil || err.Error() != errSafeStorageNotJWT {
		t.Fatalf("err = %v", err)
	}
}

func TestResolve_EncryptedBadFormatSkipsPassword(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("v11not-v10-cipher"))
	called := false
	p := &Provider{
		dbPath: writeVSCDB(t, safeStoragePrefix+raw),
		safeStoragePassword: func(context.Context, string, string) (string, error) {
			called = true
			return "", errors.New("password should not be read")
		},
	}
	_, err := p.resolve(context.Background())
	if err == nil || err.Error() != errSafeStorageFormat {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("password was read")
	}
}

func TestResolve_PlaintextSkipsSafeStorage(t *testing.T) {
	called := false
	p := &Provider{
		dbPath: writeVSCDB(t, "db-token"),
		safeStoragePassword: func(context.Context, string, string) (string, error) {
			called = true
			return "", errors.New("password should not be read")
		},
	}
	sess, err := p.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sess.token != "db-token" {
		t.Fatalf("token = %q", sess.token)
	}
	if called {
		t.Fatal("password was read")
	}
}

func TestResolve_SafeStorageMissingDoesNotFallThrough(t *testing.T) {
	cliCalled := false
	p := &Provider{
		dbPath:   writeVSCDB(t, safeStoragePrefix+safeStorageTestBlobB64),
		cliPaths: []string{writeCursorAgent(t)},
		keychain: func(context.Context) (string, error) {
			cliCalled = true
			return "cli-token", nil
		},
		safeStoragePassword: func(context.Context, string, string) (string, error) {
			return "", errors.New(errSafeStorageMissing)
		},
	}
	_, err := p.resolve(context.Background())
	if err == nil || err.Error() != errSafeStorageMissing {
		t.Fatalf("err = %v", err)
	}
	if cliCalled {
		t.Fatal("cursor-access-token was read")
	}
}

func TestSecurityFailure(t *testing.T) {
	err44 := exec.Command("sh", "-c", "exit 44").Run()
	if got := securityFailure(context.Background(), err44); got.Error() != errSafeStorageMissing {
		t.Fatalf("exit 44: %v", got)
	}
	err1 := exec.Command("sh", "-c", "exit 1").Run()
	got := securityFailure(context.Background(), err1)
	if !strings.Contains(got.Error(), errSafeStoragePrompt) {
		t.Fatalf("exit 1: %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = securityFailure(ctx, context.Canceled)
	if !strings.Contains(got.Error(), errSafeStoragePrompt) || !errors.Is(got, context.Canceled) {
		t.Fatalf("canceled: %v", got)
	}
}
