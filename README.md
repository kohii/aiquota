# aiquota

A small CLI that reports **claude / codex / cursor / copilot** subscription usage in (near) real time, using only the credentials each tool already stores locally. It deliberately requires **no extra permissions** — no browser-cookie decryption, no Full Disk Access; the only Keychain read is a single Claude item at startup.

## How it reads usage

| Provider | Credential source | API |
|---|---|---|
| codex  | `~/.codex/auth.json` (plaintext) | `chatgpt.com/backend-api/wham/usage` |
| claude | macOS Keychain item `Claude Code-credentials` | `api.anthropic.com/api/oauth/usage` |
| cursor | `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb` (read-only) | `cursor.com/api/usage-summary` |
| copilot | `~/.config/github-copilot/apps.json` (plaintext) | `api.github.com/copilot_internal/user` |

Tokens are never refreshed or rewritten. On auth failure (401) the tool tells you to re-login with the provider's own CLI/IDE (`codex login` / `claude` / Cursor IDE / VS Code Copilot).

A provider you don't use (tool not installed, or installed but not logged in) is shown as a quiet dim `– not configured` line, never as a `⚠` warning, and does not affect the exit code — so the same command works unchanged across machines with different toolsets. Genuine failures (parse errors, 401, network) still surface as `⚠`. The exit code is non-zero only when a genuine error occurred and nothing succeeded. With `--json`, a not-configured provider is `{"provider": "...", "notConfigured": true}`.

## Usage

```sh
go build -o aiquota ./cmd/aiquota

./aiquota                 # all providers, formatted
./aiquota --json          # machine-readable JSON
./aiquota codex cursor    # selected providers only
./aiquota --claude-account <name>   # pin the Claude Keychain account
```

Example output:

```
codex · plus · you@example.com
  5h limit           [████│░░░░░░░░░░░]   29.0%  · pace 23%  resets 3h49m
  Weekly limit       [███████████████│]   98.0%  · pace 99%  resets 2h8m

cursor · pro · you@example.com
  Plan (total)       [██│░░░░░░░░░░░░░]   20.6%  · $8.30 / $40.23  pace 12%  resets Jun 27 14:01
  Auto models        [█░│░░░░░░░░░░░░░]    4.1%  · pace 12%  resets Jun 27 14:01
  Named/API models   [██│█████████░░░░]   75.7%  · pace 12%  resets Jun 27 14:01

copilot · business · you
  Premium            [█░░░░░░░░░░░░░░│]    3.9%  · 11.6 / 300  pace 97%  resets 22h9m
  Chat (unlimited)
  Completions (unlimited)
```

The `│` inside each bar is a **pace marker**: it shows how far the current reset window has elapsed (`pace NN%` is that elapsed fraction). **If the filled bar is to the left of the marker you're ahead of schedule; to the right you're burning faster than time.** For example, codex Weekly at 98% used with 99% elapsed is right on pace, while cursor's Plan at 20.6% used with only 12% elapsed is running a bit hot.

## Quick access (Raycast / launchers)

Install the binary first — every option below shells out to it:

```sh
go install github.com/kohii/aiquota/cmd/aiquota@latest
```

### Raycast extension (recommended)

A native Raycast extension lives in [`raycast/`](raycast/). Unlike a Script
Command — whose `fullOutput` view is plain text and ignores ANSI color — the
extension renders a proper list with **colored progress rings, percentage tags,
and reset times** (red/yellow/green by the same thresholds as the CLI). It runs
`aiquota --json` and is a thin display layer, so the Go providers stay the
single source of truth.

```sh
cd raycast
npm install
npm run dev      # imports it into Raycast; stays installed after you stop dev
```

Then run **AI Usage** from Raycast (bind a hotkey if you like). `⌘D` toggles a
detail pane (raw counts, window-elapsed %, source, fetch time); `⌘R` refreshes.
The binary is auto-detected (`~/go/bin`, Homebrew, `/usr/local/bin`,
`~/.local/bin`, then `PATH`); set an explicit path in the extension preferences
if yours lives elsewhere.

### Script Command (plain text)

If you'd rather not build the extension, any launcher that runs a command works
(Raycast Script Command, Alfred, SwiftBar/xbar). The output is plain monospace
text:

```bash
#!/bin/bash
# @raycast.schemaVersion 1
# @raycast.title AI Usage
# @raycast.mode fullOutput
# @raycast.packageName aiquota
# @raycast.icon 📊
export PATH="$HOME/go/bin:$PATH"
exec aiquota
```

## Design

See [docs/design.md](docs/design.md) for the investigation, data sources, and design decisions.

## Tests

```sh
go test ./...
```

Each provider's normalization logic is a pure function (`parse.go`) tested against fixtures derived from real API response shapes.

## Acknowledgements

Inspired by [CodexBar](https://github.com/steipete/CodexBar) (MIT) by Peter Steinberger. CodexBar was used as a reference for *where* each provider exposes usage (file paths, endpoints, cookie/token shapes); no source code was copied — this is an independent Go implementation intentionally pared down to the lowest-privilege local path for a single Mac.
