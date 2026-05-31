// Package usage defines the provider-agnostic usage model shared across
// providers (codex / claude / cursor / copilot) and consumed by the CLI renderer.
package usage

import (
	"context"
	"fmt"
	"time"
)

// Unit describes what a Meter's numeric values are measured in.
type Unit string

const (
	UnitPercent  Unit = "percent"
	UnitUSD      Unit = "usd"
	UnitCredits  Unit = "credits"
	UnitRequests Unit = "requests"
)

// Meter is one measurable axis of usage: a time-window quota, a cost, a
// balance, or a request count. Providers normalize their (differing) payloads
// into a slice of Meters so the renderer and JSON output stay uniform.
//
// Pointer fields are nil when the provider does not report that value. Unknown
// quotas the provider added server-side are passed through with Known=false so
// they are never silently dropped.
type Meter struct {
	Key         string     `json:"key"`
	Label       string     `json:"label"`
	UsedPercent *float64   `json:"usedPercent,omitempty"`
	Used        *float64   `json:"used,omitempty"`
	Limit       *float64   `json:"limit,omitempty"`
	Remaining   *float64   `json:"remaining,omitempty"`
	Unit        Unit       `json:"unit,omitempty"`
	Currency    string     `json:"currency,omitempty"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
	// WindowStart is when the current quota window opened. Together with
	// ResetsAt it lets the renderer show how far through the window "now" is
	// (the pace marker), so usage can be compared against elapsed time.
	WindowStart *time.Time `json:"windowStart,omitempty"`
	// Unlimited marks a quota the provider reports as having no cap (e.g. GitHub
	// Copilot Chat/Completions on a paid plan). It is distinct from "the provider
	// did not report a percent": consumers must not infer unlimited from a nil
	// UsedPercent. When true there is no bar/percent to show.
	Unlimited bool `json:"unlimited,omitempty"`
	Known     bool `json:"known"`
}

// Usage is one provider's normalized snapshot.
type Usage struct {
	Provider  string    `json:"provider"`
	Account   string    `json:"account,omitempty"`
	Plan      string    `json:"plan,omitempty"`
	Meters    []Meter   `json:"meters"`
	Source    string    `json:"source,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// Provider fetches a usage snapshot for a single service.
type Provider interface {
	Name() string
	Fetch(ctx context.Context) (*Usage, error)
}

// ReauthError signals that local credentials are present but rejected by the
// API (401/403). The fix is to re-login with the provider's own CLI/IDE; this
// tool deliberately does not refresh or rewrite tokens in its initial version.
type ReauthError struct {
	Provider string
	Hint     string
}

func (e *ReauthError) Error() string {
	if e.Hint == "" {
		return fmt.Sprintf("%s: 認証が無効です（再ログインしてください）", e.Provider)
	}
	return fmt.Sprintf("%s: 認証が無効です。%s", e.Provider, e.Hint)
}

// NotConfiguredError signals that a provider's local credential source is
// simply absent — the tool is not installed, or installed but never logged in
// — as opposed to a genuine failure (parse error, 401, network). The CLI shows
// it as a quiet "not configured" line rather than a warning, and it does not
// affect the exit code: a machine without some of the tools is not an error.
type NotConfiguredError struct {
	Provider string
	Reason   string // optional short reason, e.g. "not logged in"
}

func (e *NotConfiguredError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s: %s", e.Provider, e.Reason)
	}
	return fmt.Sprintf("%s: not configured", e.Provider)
}

// Ptr returns a pointer to v. Handy for building Meter fields inline.
func Ptr[T any](v T) *T { return &v }
