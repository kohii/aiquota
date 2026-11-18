// Package cursor fetches Cursor subscription usage.
//
// The session token comes from the Cursor IDE store (state.vscdb, key
// cursorAuth/accessToken), opened read-only and immutable. When that file is
// missing or the token is empty, and the cursor-agent binary is installed, the
// token comes from the macOS Keychain item cursor-access-token. The keychain
// is not read when cursor-agent is not installed.
//
// The usage endpoint authenticates with a session cookie:
//
//	WorkosCursorSessionToken=<jwt.sub>::<accessToken>
//
// with "::" URL-encoded. Bearer auth and a raw cookie return 401.
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
	"os/exec"
	"path/filepath"
	"runtime"
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

	sourceVSCDB     = "vscdb:state.vscdb"
	sourceKeychain  = "keychain:cursor-access-token"
	keychainService = "cursor-access-token"
	keychainAccount = "cursor-user"

	// securityBin is the macOS Keychain CLI. Use its absolute path so it resolves
	// regardless of the caller's PATH: launchers like Raycast pass a minimal PATH
	// that omits /usr/bin, which would otherwise make `security` "not found".
	securityBin = "/usr/bin/security"
)

// Provider implements usage.Provider for Cursor.
type Provider struct {
	http   *httpx.Client
	dbPath string
	// cliPaths are candidate cursor-agent binaries. New fills in the defaults.
	cliPaths []string
	// keychain reads the Cursor CLI access token. Nil runs /usr/bin/security.
	// A non-nil reader is used on any OS so tests do not depend on darwin.
	keychain func(ctx context.Context) (string, error)
	// goos overrides runtime.GOOS in tests. Empty means the host OS.
	goos string
}

// session is the one credential value Fetch uses.
type session struct {
	token, email, membership, source string
}

// New returns a Cursor provider. It reads state.vscdb, then cursor-agent
// (~/.local/bin/cursor-agent and PATH), then the keychain on macOS.
func New() *Provider {
	p := &Provider{http: httpx.New()}
	if h, err := os.UserHomeDir(); err == nil {
		p.dbPath = filepath.Join(h, "Library", "Application Support", "Cursor",
			"User", "globalStorage", "state.vscdb")
		p.cliPaths = append(p.cliPaths, filepath.Join(h, ".local", "bin", "cursor-agent"))
	}
	if path, err := exec.LookPath("cursor-agent"); err == nil {
		p.cliPaths = append(p.cliPaths, path)
	}
	return p
}

func (p *Provider) Name() string { return "cursor" }

// Fetch reads the local session token and queries the usage-summary endpoint.
func (p *Provider) Fetch(ctx context.Context) (*usage.Usage, error) {
	sess, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	sub, err := jwtSubject(sess.token)
	if err != nil {
		return nil, err
	}
	cookie := "WorkosCursorSessionToken=" + url.QueryEscape(sub) + "%3A%3A" + sess.token

	body, err := p.http.Get(ctx, usageURL, map[string]string{"Cookie": cookie})
	if err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) && se.Unauthorized() {
			return nil, &usage.ReauthError{Provider: "cursor", Hint: reauthHint(sess.source)}
		}
		return nil, err
	}
	u, err := parseUsage(body)
	if err != nil {
		return nil, err
	}
	if u.Account == "" {
		u.Account = sess.email
	}
	if u.Plan == "" {
		u.Plan = sess.membership
	}
	u.Source = sess.source
	u.FetchedAt = time.Now()
	return u, nil
}

// vscdbState is how far the IDE store got. Missing and empty stay distinct
// so a logged-out IDE still says so when the CLI is not there to try next.
type vscdbState int

const (
	vscdbMissing vscdbState = iota
	vscdbNoToken
	vscdbOK
)

const ideLoggedOut = "ログインされていません（Cursor IDE）"

// resolve picks one session. state.vscdb wins. The real keychain command runs
// only when cursor-agent is installed, and only on darwin.
func (p *Provider) resolve(ctx context.Context) (session, error) {
	sess, state, err := readVSCDB(p.dbPath)
	if err != nil {
		return session{}, err
	}
	if state == vscdbOK {
		return sess, nil
	}

	if p.keychainAllowed() {
		return p.sessionFromKeychain(ctx)
	}
	if state == vscdbNoToken {
		return session{}, &usage.NotConfiguredError{Provider: "cursor", Reason: ideLoggedOut}
	}
	return session{}, &usage.NotConfiguredError{Provider: "cursor"}
}

// keychainAllowed reports whether resolve may read a CLI token. An injected
// reader is allowed on any OS. The real security command is darwin-only.
func (p *Provider) keychainAllowed() bool {
	if !cursorAgentInstalled(p.cliPaths) {
		return false
	}
	if p.keychain != nil {
		return true
	}
	return p.hostOS() == "darwin"
}

func (p *Provider) hostOS() string {
	if p.goos != "" {
		return p.goos
	}
	return runtime.GOOS
}

func (p *Provider) sessionFromKeychain(ctx context.Context) (session, error) {
	read := p.keychain
	if read == nil {
		read = readCursorKeychain
	}
	token, err := read(ctx)
	if err != nil {
		return session{}, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return session{}, &usage.NotConfiguredError{
			Provider: "cursor",
			Reason:   "ログインされていません（`agent login`）",
		}
	}
	return session{
		token:  token,
		email:  cursorCLIEmail(),
		source: sourceKeychain,
	}, nil
}

func reauthHint(source string) string {
	if source == sourceKeychain {
		return "`agent login` を実行してください"
	}
	return "Cursor IDE で再ログインしてください"
}

// readVSCDB reads token, email, and membership. A missing file is vscdbMissing.
// A file with no token is vscdbNoToken. A SQLite error is returned as-is.
func readVSCDB(path string) (session, vscdbState, error) {
	if path == "" {
		return session{}, vscdbMissing, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return session{}, vscdbMissing, nil
		}
		return session{}, vscdbMissing, fmt.Errorf("cursor state.vscdb を確認できません: %w", err)
	}

	dsn := "file:" + path + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return session{}, vscdbMissing, fmt.Errorf("cursor state.vscdb を開けません: %w", err)
	}
	defer db.Close()

	var token string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", tokenKey).Scan(&token)
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && strings.TrimSpace(token) == ""):
		return session{}, vscdbNoToken, nil
	case err != nil:
		return session{}, vscdbMissing, fmt.Errorf("cursor state.vscdb を読めません: %w", err)
	}

	return session{
		token:      strings.TrimSpace(token),
		email:      itemValue(db, emailKey),
		membership: itemValue(db, membershipKey),
		source:     sourceVSCDB,
	}, vscdbOK, nil
}

func itemValue(db *sql.DB, key string) string {
	var v string
	_ = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", key).Scan(&v)
	return v
}

// cursorAgentInstalled reports whether any path is an executable file after
// symlink resolution. It looks only at the given paths, never at a binary
// named "agent".
func cursorAgentInstalled(paths []string) bool {
	for _, path := range paths {
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return true
		}
	}
	return false
}

func readCursorKeychain(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, securityBin,
		"find-generic-password", "-s", keychainService, "-a", keychainAccount, "-w").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 {
			return "", &usage.NotConfiguredError{
				Provider: "cursor",
				Reason:   "ログインされていません（`agent login`）",
			}
		}
		return "", fmt.Errorf("cursor の Keychain 認証情報を読めません: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// cursorCLIEmail reads authInfo.email from the Cursor CLI config. A missing
// or unreadable file yields an empty email. The file is not a token source.
func cursorCLIEmail() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(home, ".cursor", "cli-config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		AuthInfo struct {
			Email string `json:"email"`
		} `json:"authInfo"`
	}
	if json.Unmarshal(body, &cfg) != nil {
		return ""
	}
	return cfg.AuthInfo.Email
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
