package cursor

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const (
	safeStoragePrefix  = "cursor-safe-storage:v1:"
	safeStorageService = "Cursor Safe Storage"
	safeStorageAccount = "Cursor Key"

	// Chromium macOS os_crypt, which Electron safeStorage.encryptString uses.
	// The Keychain item holds a password. v10 ciphertext is AES-128-CBC with
	// this KDF and a fixed IV of 16 0x20 bytes.
	v10Prefix    = "v10"
	pbkdf2Salt   = "saltysalt"
	pbkdf2Iter   = 1003
	pbkdf2KeyLen = 16
)

const (
	errSafeStorageOS      = "暗号化された cursor accessToken はこの OS では復号できません"
	errSafeStorageFormat  = "cursor accessToken の暗号化形式に対応していません"
	errSafeStorageDecrypt = "cursor accessToken を復号できません"
	errSafeStorageNotJWT  = "cursor accessToken を復号しましたが JWT ではありません"
	errSafeStorageMissing = "cursor accessToken は暗号化されていますが、Keychain 項目 Cursor Safe Storage がありません"
	errSafeStorageEmpty   = "cursor の Keychain 項目 Cursor Safe Storage が空です"
	errSafeStoragePrompt  = "cursor の Keychain 許可ダイアログを確認してください"
)

// unwrapStoredToken returns the plaintext session JWT. A value without the
// safeStorage prefix is already plaintext. An encrypted value is decrypted
// here, and must be a JWT before it is used as a session.
func (p *Provider) unwrapStoredToken(ctx context.Context, stored string) (string, error) {
	if !strings.HasPrefix(stored, safeStoragePrefix) {
		return stored, nil
	}
	if p.safeStoragePassword == nil && p.hostOS() != "darwin" {
		return "", errors.New(errSafeStorageOS)
	}
	raw, err := base64.StdEncoding.DecodeString(stored[len(safeStoragePrefix):])
	if err != nil {
		return "", fmt.Errorf("%s: %w", errSafeStorageDecrypt, err)
	}
	if _, err := v10Ciphertext(raw); err != nil {
		return "", err
	}
	read := p.safeStoragePassword
	if read == nil {
		read = readSafeStoragePassword
	}
	password, err := read(ctx, safeStorageService, safeStorageAccount)
	if err != nil {
		return "", err
	}
	plain, err := decryptSafeStorage(raw, password)
	if err != nil {
		return "", err
	}
	if _, err := jwtSubject(plain); err != nil {
		return "", errors.New(errSafeStorageNotJWT)
	}
	return plain, nil
}

func readSafeStoragePassword(ctx context.Context, service, account string) (string, error) {
	out, err := exec.CommandContext(ctx, securityBin,
		"find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		return "", securityFailure(ctx, err)
	}
	password := strings.TrimSpace(string(out))
	if password == "" {
		return "", errors.New(errSafeStorageEmpty)
	}
	return password, nil
}

func securityFailure(ctx context.Context, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 44 {
		return errors.New(errSafeStorageMissing)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return fmt.Errorf("%s: %w", errSafeStoragePrompt, err)
}

func decryptSafeStorage(blob []byte, password string) (string, error) {
	ct, err := v10Ciphertext(blob)
	if err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha1.New, password, []byte(pbkdf2Salt), pbkdf2Iter, pbkdf2KeyLen)
	if err != nil {
		return "", fmt.Errorf("%s: %w", errSafeStorageDecrypt, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("%s: %w", errSafeStorageDecrypt, err)
	}
	plain := make([]byte, len(ct))
	iv := bytes.Repeat([]byte{0x20}, aes.BlockSize)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return "", errors.New(errSafeStorageDecrypt)
	}
	return string(plain), nil
}

func v10Ciphertext(raw []byte) ([]byte, error) {
	if !bytes.HasPrefix(raw, []byte(v10Prefix)) {
		return nil, errors.New(errSafeStorageFormat)
	}
	ct := raw[len(v10Prefix):]
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errors.New(errSafeStorageDecrypt)
	}
	return ct, nil
}

func pkcs7Unpad(b []byte, block int) ([]byte, error) {
	if len(b) == 0 || len(b)%block != 0 {
		return nil, errors.New("bad padding")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > block || n > len(b) {
		return nil, errors.New("bad padding")
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errors.New("bad padding")
		}
	}
	return b[:len(b)-n], nil
}
