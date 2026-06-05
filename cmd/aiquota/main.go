// Command aiquota reports claude / codex / cursor / copilot subscription usage
// by reading each tool's local credentials (no extra permissions) and querying
// its usage API. Prints a compact summary, or machine-readable JSON with --json.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/kohii/aiquota/internal/provider/claude"
	"github.com/kohii/aiquota/internal/provider/codex"
	"github.com/kohii/aiquota/internal/provider/copilot"
	"github.com/kohii/aiquota/internal/provider/cursor"
	"github.com/kohii/aiquota/internal/render"
	"github.com/kohii/aiquota/internal/usage"
)

func main() {
	var (
		jsonOut       bool
		style         string
		claudeAccount string
		timeout       time.Duration
	)
	flag.BoolVar(&jsonOut, "json", false, "JSON で出力する")
	flag.StringVar(&style, "style", "auto", "出力スタイル: auto（TTYなら色）/ plain / emoji（信号絵文字 🟢🟡🔴）")
	flag.StringVar(&claudeAccount, "claude-account", "", "Claude の Keychain account 名（省略時は service のみで照合）")
	flag.DurationVar(&timeout, "timeout", 30*time.Second, "全体のタイムアウト")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: aiquota [flags] [provider...]\n\n")
		fmt.Fprintf(os.Stderr, "providers: codex claude cursor copilot (省略時は全部)\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	all := map[string]usage.Provider{
		"codex":   codex.New(),
		"claude":  claude.New(claudeAccount),
		"cursor":  cursor.New(),
		"copilot": copilot.New(),
	}

	providers, err := selectProviders(all, flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	// Resolve the output style before fetching so an invalid --style fails fast,
	// without first reading the Keychain or hitting provider APIs.
	opt, err := renderOptions(style, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	results := fetchAll(ctx, providers)

	if jsonOut {
		writeJSON(os.Stdout, results)
	} else {
		render.Render(os.Stdout, results, opt)
	}

	if shouldFail(results) {
		os.Exit(1)
	}
}

// renderOptions maps the --style flag to render.Options. "auto" keeps the
// original behavior (ANSI color only on a TTY); "emoji" trades ANSI for a
// leading signal glyph so launchers that show plain monospaced text (Raycast's
// fullOutput) still convey the usage level.
func renderOptions(style string, out *os.File) (render.Options, error) {
	switch style {
	case "", "auto":
		return render.Options{Color: isTerminal(out)}, nil
	case "plain":
		return render.Options{}, nil
	case "emoji":
		return render.Options{Emoji: true}, nil
	default:
		return render.Options{}, fmt.Errorf("不明な --style: %q (auto/plain/emoji)", style)
	}
}

// selectProviders returns the requested providers in a stable order, or all of
// them (codex, claude, cursor, copilot) when no names are given.
func selectProviders(all map[string]usage.Provider, names []string) ([]usage.Provider, error) {
	order := []string{"codex", "claude", "cursor", "copilot"}
	if len(names) > 0 {
		order = nil
		seen := map[string]bool{}
		for _, n := range names {
			if _, ok := all[n]; !ok {
				return nil, fmt.Errorf("不明なプロバイダ: %q (codex/claude/cursor/copilot)", n)
			}
			if !seen[n] {
				order = append(order, n)
				seen[n] = true
			}
		}
	}
	out := make([]usage.Provider, 0, len(order))
	for _, n := range order {
		out = append(out, all[n])
	}
	return out, nil
}

// fetchAll queries every provider concurrently, preserving input order.
func fetchAll(ctx context.Context, providers []usage.Provider) []render.Result {
	results := make([]render.Result, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func(i int, p usage.Provider) {
			defer wg.Done()
			u, err := p.Fetch(ctx)
			results[i] = render.Result{Name: p.Name(), Usage: u, Err: err}
		}(i, p)
	}
	wg.Wait()
	return results
}

// shouldFail reports a non-zero exit only when a genuine error occurred and
// nothing succeeded. A "not configured" provider (tool absent / not logged in)
// is neutral: a machine without some of the tools is not a failure.
func shouldFail(results []render.Result) bool {
	var success, realErr bool
	for _, r := range results {
		switch {
		case r.Err == nil && r.Usage != nil:
			success = true
		case r.Err != nil:
			var nc *usage.NotConfiguredError
			if !errors.As(r.Err, &nc) {
				realErr = true
			}
		}
	}
	return realErr && !success
}

// jsonResult is the JSON shape: usage on success, notConfigured when the tool
// is absent / logged out, or an error string on a genuine failure.
type jsonResult struct {
	Provider      string       `json:"provider"`
	Usage         *usage.Usage `json:"usage,omitempty"`
	NotConfigured bool         `json:"notConfigured,omitempty"`
	Error         string       `json:"error,omitempty"`
}

func writeJSON(w io.Writer, results []render.Result) {
	out := make([]jsonResult, len(results))
	for i, r := range results {
		jr := jsonResult{Provider: r.Name, Usage: r.Usage}
		if r.Err != nil {
			var nc *usage.NotConfiguredError
			if errors.As(r.Err, &nc) {
				jr.NotConfigured = true
			} else {
				jr.Error = r.Err.Error()
			}
		}
		out[i] = jr
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

// isTerminal reports whether f is a character device (a TTY), used to decide
// whether to emit ANSI color.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
