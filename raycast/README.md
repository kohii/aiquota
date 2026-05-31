# AI Usage (Raycast extension)

A Raycast view for [`aiquota`](../): claude / codex / cursor / copilot
subscription usage with colored progress rings, percentage tags, and reset
times — the color the Script Command can't show.

It's a thin display layer: it runs `aiquota --json` and renders the result, so
the Go providers stay the single source of truth. Pace (window-elapsed %),
reset formatting, and the red/yellow/green thresholds mirror the CLI renderer
and are unit-tested in [`src/lib.ts`](src/lib.ts).

## Install

The `aiquota` binary must be installed first:

```sh
go install github.com/kohii/aiquota/cmd/aiquota@latest
```

Then load the extension into Raycast (development mode persists it after you
stop the dev server):

```sh
npm install
npm run dev
```

Run **AI Usage** from Raycast. The binary is auto-detected (`~/go/bin`,
`/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, then `PATH`); set an
explicit path in the extension preferences if needed.

- `⌘R` — refresh
- `⌘D` — toggle the detail pane (raw counts, window-elapsed %, source, fetch time)

Unlimited quotas (e.g. Copilot Chat/Completions on a paid plan) show a full-signal
marker instead of a bar; "not configured" providers (tool absent / logged out)
show a dim line; genuine failures show a red warning.

## Develop

```sh
npm test       # vitest — pure logic in src/lib.ts
npm run lint   # ray lint (ESLint + Prettier)
npm run build  # ray build (type-check + bundle)
```
