// Command aiquota reports claude / codex / cursor / copilot subscription usage
// by reading each tool's local credentials (no extra permissions) and querying
// its usage API. Prints a compact summary, or machine-readable JSON with --json.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
		claudeAccount string
		timeout       time.Duration
	)
	flag.BoolVar(&jsonOut, "json", false, "JSON で出力する")
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

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	results := fetchAll(ctx, providers)

	if jsonOut {
		writeJSON(os.Stdout, results)
	} else {
		render.Render(os.Stdout, results, isTerminal(os.Stdout))
	}

	if !anySuccess(results) {
		os.Exit(1)
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

func anySuccess(results []render.Result) bool {
	for _, r := range results {
		if r.Err == nil && r.Usage != nil {
			return true
		}
	}
	return false
}

// jsonResult is the JSON shape: usage on success, error string on failure.
type jsonResult struct {
	Provider string       `json:"provider"`
	Usage    *usage.Usage `json:"usage,omitempty"`
	Error    string       `json:"error,omitempty"`
}

func writeJSON(w *os.File, results []render.Result) {
	out := make([]jsonResult, len(results))
	for i, r := range results {
		jr := jsonResult{Provider: r.Name, Usage: r.Usage}
		if r.Err != nil {
			jr.Error = r.Err.Error()
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
