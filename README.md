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

A provider you don't use (tool not installed, or installed but not logged in) is shown as a quiet dim `– not configured` line, never as a `⚠` warning, and does not affect the exit code — so the same command works unchanged across machines with different toolsets. Genuine failures (parse errors, 401, network) still surface as `⚠`. The exit code is non-zero only when a genuine error occurred and nothing succeeded. With `--json`, a not-configured provider is `{"provider": "...", "notConfigured": true}`. The account (email/login) is shown only in `--json`; the formatted output omits it to avoid leaking it in a shared terminal or screenshot.

## Usage

```sh
go build -o aiquota ./cmd/aiquota

./aiquota                 # all providers, formatted (ANSI color on a TTY)
./aiquota --json          # machine-readable JSON
./aiquota --style emoji   # plain text with 🟢🟡🔴 signals (for launchers that ignore ANSI)
./aiquota codex cursor    # selected providers only
./aiquota --claude-account <name>   # pin the Claude Keychain account
```

Example output (color is shown here as the leading 🔵🟢🟡🔴; on a TTY it is ANSI bar color instead):

```
codex · plus
🔵 5h limit           [█░░░░░░░░░│░░░░░]    9.0%  · pace 69%  proj 13%  resets 1h31m
🟢 Weekly limit       [███░│░░░░░░░░░░░]   17.0%  · pace 19%  resets 2d4h

cursor · pro
🟢 Plan (total)       [████░│░░░░░░░░░░]   22.2%  · $9.64 / $43.36  pace 30%  proj 74%  resets Jun 27 14:01
🔵 Auto models        [█░░░░│░░░░░░░░░░]    4.1%  · pace 30%  proj 14%  resets Jun 27 14:01
🔴 Named/API models   [█████│█████░░░░░]   82.7%  · pace 30%  proj 276%  resets Jun 27 14:01

copilot · business
🟢 Premium            [████░░░░░░░░░░░│]   85.0%  · 255 / 300  pace 97%  proj 88%  resets 22h9m
⚪ Chat (unlimited)
⚪ Completions (unlimited)
```

These are **flat-rate, use-it-or-lose-it** quotas, so the color answers *"are you on track to get your money's worth before it resets?"* — not just *"how full is the bar?"*. Each meter is colored by its usage **projected to the reset** (`proj NN%` = if you keep this pace, how much you'll have used when the window resets):

- 🔵 **blue** — tracking to finish well under the cap. You're paying for headroom you won't touch: *use more or lose it.*
- 🟢 **green** — on track to use most of the window.
- 🟡 **yellow** — tracking to run out somewhat early.
- 🔴 **red** — nearly spent right now, or tracking to run out well before reset.

The `│` inside each bar is the **pace marker**: how far the current reset window has elapsed (`pace NN%`). The bigger the gap with the filled bar to its **left**, the more you're leaving on the table (the blue case); filled **past** the marker means you're burning faster than time. Early in a window (before ~20–25% elapsed) the projection is too noisy to trust, so a meter with no clock or a barely-started window falls back to plain "how full" coloring.

## Quick access (Raycast / launchers)

Install the binary first:

```sh
go install github.com/kohii/aiquota/cmd/aiquota@latest
```

Any launcher that runs a command works (Raycast Script Command, Alfred,
SwiftBar/xbar). Launchers show plain monospace text and ignore ANSI color, so
use `--style emoji`: the bars, pace markers, and reset times stay, and a leading
🔵🟢🟡🔴 conveys the same pace-based level the ANSI color otherwise would (🔵 use
more or lose it · 🟢 on track · 🟡 running out a bit early · 🔴 nearly spent;
⚪ marks uncapped or unreported quotas). Because every line starts with a
same-width glyph, the bars stay aligned even though emoji are full-width.

```bash
#!/bin/bash
# @raycast.schemaVersion 1
# @raycast.title AI Usage
# @raycast.mode fullOutput
# @raycast.packageName aiquota
# @raycast.icon 📊
export PATH="$HOME/go/bin:$PATH"
exec aiquota --style emoji
```

> A native React/TS Raycast extension was prototyped but removed: its `List`
> view turned each meter into a large card and lost the CLI's at-a-glance
> density and pace marker. `--style emoji` brings the color back to the compact
> text view, keeping `internal/render` the single source of truth.

## Design

See [docs/design.md](docs/design.md) for the investigation, data sources, and design decisions.

## Tests

```sh
go test ./...
```

Each provider's normalization logic is a pure function (`parse.go`) tested against fixtures derived from real API response shapes.

## Acknowledgements

Inspired by [CodexBar](https://github.com/steipete/CodexBar) (MIT) by Peter Steinberger. CodexBar was used as a reference for *where* each provider exposes usage (file paths, endpoints, cookie/token shapes); no source code was copied — this is an independent Go implementation intentionally pared down to the lowest-privilege local path for a single Mac.
