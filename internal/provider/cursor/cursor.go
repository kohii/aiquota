// Package cursor fetches Cursor IDE subscription usage.
//
// Credentials are read from the Cursor IDE's local SQLite store
// (state.vscdb, key cursorAuth/accessToken) opened read-only/immutable, so no
// browser cookie decryption and no Full Disk Access are needed. The endpoint
// authenticates via a session cookie of the form
//
//	WorkosCursorSessionToken=<jwt.sub>::<accessToken>
//
// (the "::" URL-encoded). Verified empirically; Bearer / raw cookie return 401.
package cursor

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the cgo-free "sqlite" driver

	"github.com/kohii/aiquota/internal/httpx"
	"github.com/kohii/aiquota/internal/usage"
)

const (
	usageURL      = "https://cursor.com/api/usage-summary"
	tokenKey      = "cursorAuth/accessToken"
	emailKey      = "cursorAuth/cachedEmail"
	membershipKey = "cursorAuth/stripeMembershipType"
)

// Provider implements usage.Provider for Cursor.
type Provider struct {
	http   *httpx.Client
	dbPath string
}

// New returns a Cursor provider reading the default state.vscdb location.
func New() *Provider {
	var dbPath string
	if h, err := os.UserHomeDir(); err == nil {
		dbPath = filepath.Join(h, "Library", "Application Support", "Cursor",
			"User", "globalStorage", "state.vscdb")
	}
	return &Provider{http: httpx.New(), dbPath: dbPath}
}

func (p *Provider) Name() string { return "cursor" }

// Fetch reads the local session token and queries the usage-summary endpoint.
func (p *Provider) Fetch(ctx context.Context) (*usage.Usage, error) {
	creds, err := readLocalState(p.dbPath)
	if err != nil {
		return nil, err
	}
	sub, err := jwtSubject(creds.token)
	if err != nil {
		return nil, err
	}
	cookie := "WorkosCursorSessionToken=" + url.QueryEscape(sub) + "%3A%3A" + creds.token

	body, err := p.http.Get(ctx, usageURL, map[string]string{"Cookie": cookie})
	if err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) && se.Unauthorized() {
			return nil, &usage.ReauthError{Provider: "cursor", Hint: "Cursor IDE で再ログインしてください"}
		}
		return nil, err
	}
	u, err := parseUsage(body)
	if err != nil {
		return nil, err
	}
	if u.Account == "" {
		u.Account = creds.email
	}
	if u.Plan == "" {
		u.Plan = creds.membership
	}
	u.Source = "vscdb:state.vscdb"
	u.FetchedAt = time.Now()
	return u, nil
}

type localState struct {
	token      string
	email      string
	membership string
}

// readLocalState reads token/email/membership from state.vscdb without
// disturbing Cursor's writes (immutable + read-only).
func readLocalState(path string) (*localState, error) {
	if path == "" {
		return nil, errors.New("cursor の state.vscdb パスを特定できません")
	}
	// A missing store means Cursor isn't installed -> not configured, not error.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, &usage.NotConfiguredError{Provider: "cursor"}
	}
	dsn := "file:" + path + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("cursor state.vscdb を開けません: %w", err)
	}
	defer db.Close()

	// optional reads a value, ignoring "row not found" but surfacing real errors.
	get := func(key string) string {
		var v string
		_ = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", key).Scan(&v)
		return v
	}

	var token string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", tokenKey).Scan(&token)
	switch {
	case err == sql.ErrNoRows || (err == nil && strings.TrimSpace(token) == ""):
		// Store exists but holds no token -> Cursor installed, not logged in.
		return nil, &usage.NotConfiguredError{Provider: "cursor", Reason: "ログインされていません（Cursor IDE）"}
	case err != nil:
		return nil, fmt.Errorf("cursor state.vscdb を読めません: %w", err)
	}

	return &localState{
		token:      strings.TrimSpace(token),
		email:      get(emailKey),
		membership: get(membershipKey),
	}, nil
}

// jwtSubject decodes the "sub" claim from a JWT without verifying it. The sub
// is used as the WorkosCursorSessionToken prefix.
func jwtSubject(tok string) (string, error) {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return "", errors.New("cursor accessToken が JWT 形式ではありません")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("cursor accessToken の payload を復号できません: %w", err)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("cursor accessToken の payload を解析できません: %w", err)
	}
	if claims.Sub == "" {
		return "", errors.New("cursor accessToken に sub クレームがありません")
	}
	return claims.Sub, nil
}
