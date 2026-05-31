package main

import (
	"context"
	"testing"

	"github.com/kohii/aiquota/internal/usage"
)

type stubProvider struct{ name string }

func (s stubProvider) Name() string                                { return s.name }
func (s stubProvider) Fetch(context.Context) (*usage.Usage, error) { return nil, nil }

func names(ps []usage.Provider) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name()
	}
	return out
}

func TestSelectProviders(t *testing.T) {
	all := map[string]usage.Provider{
		"codex":   stubProvider{"codex"},
		"claude":  stubProvider{"claude"},
		"cursor":  stubProvider{"cursor"},
		"copilot": stubProvider{"copilot"},
	}

	t.Run("default is all in fixed order", func(t *testing.T) {
		got, err := selectProviders(all, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"codex", "claude", "cursor", "copilot"}
		if g := names(got); !equal(g, want) {
			t.Errorf("got %v, want %v", g, want)
		}
	})

	t.Run("subset preserves request order and dedups", func(t *testing.T) {
		got, err := selectProviders(all, []string{"cursor", "codex", "cursor"})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"cursor", "codex"}
		if g := names(got); !equal(g, want) {
			t.Errorf("got %v, want %v", g, want)
		}
	})

	t.Run("unknown provider errors", func(t *testing.T) {
		if _, err := selectProviders(all, []string{"gemini"}); err == nil {
			t.Error("expected error for unknown provider")
		}
	})
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
