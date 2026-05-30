# aiquota

A small CLI that reports **claude / codex / cursor** subscription usage in (near) real time, using only the credentials each tool already stores locally. It deliberately requires **no extra permissions** — no browser-cookie decryption, no Full Disk Access; the only Keychain read is a single Claude item at startup.

## How it reads usage

| Provider | Credential source | API |
|---|---|---|
| codex  | `~/.codex/auth.json` (plaintext) | `chatgpt.com/backend-api/wham/usage` |
| claude | macOS Keychain item `Claude Code-credentials` | `api.anthropic.com/api/oauth/usage` |
| cursor | `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb` (read-only) | `cursor.com/api/usage-summary` |

Tokens are never refreshed or rewritten. On auth failure (401) the tool tells you to re-login with the provider's own CLI/IDE (`codex login` / `claude` / Cursor IDE).

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
```

The `│` inside each bar is a **pace marker**: it shows how far the current reset window has elapsed (`pace NN%` is that elapsed fraction). **If the filled bar is to the left of the marker you're ahead of schedule; to the right you're burning faster than time.** For example, codex Weekly at 98% used with 99% elapsed is right on pace, while cursor's Plan at 20.6% used with only 12% elapsed is running a bit hot.

## Design

See [docs/design.md](docs/design.md) for the investigation, data sources, and design decisions.

## Tests

```sh
go test ./...
```

Each provider's normalization logic is a pure function (`parse.go`) tested against fixtures derived from real API response shapes.

## Acknowledgements

Inspired by [CodexBar](https://github.com/steipete/CodexBar) (MIT) by Peter Steinberger. CodexBar was used as a reference for *where* each provider exposes usage (file paths, endpoints, cookie/token shapes); no source code was copied — this is an independent Go implementation intentionally pared down to the lowest-privilege local path for a single Mac.
