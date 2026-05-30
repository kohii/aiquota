// Package usage defines the provider-agnostic usage model shared across
// providers (codex / claude / cursor) and consumed by the CLI renderer.
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
	Known       bool       `json:"known"`
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

// Ptr returns a pointer to v. Handy for building Meter fields inline.
func Ptr[T any](v T) *T { return &v }
