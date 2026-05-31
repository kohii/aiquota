package copilot

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/kohii/aiquota/internal/usage"
)

// copilotResponse mirrors the fields of GET /copilot_internal/user that we use.
type copilotResponse struct {
	Login             string                   `json:"login"`
	CopilotPlan       string                   `json:"copilot_plan"`
	QuotaResetDateUTC string                   `json:"quota_reset_date_utc"`
	QuotaResetDate    string                   `json:"quota_reset_date"`
	QuotaSnapshots    map[string]quotaSnapshot `json:"quota_snapshots"`
}

// quotaSnapshot is one quota line (premium interactions, chat, completions, …).
// Unlimited quotas report unlimited=true with zeroed counts; metered ones carry
// entitlement/remaining and a percent_remaining.
type quotaSnapshot struct {
	Unlimited        bool     `json:"unlimited"`
	PercentRemaining *float64 `json:"percent_remaining"`
	Entitlement      *float64 `json:"entitlement"`
	Remaining        *float64 `json:"remaining"`
	QuotaRemaining   *float64 `json:"quota_remaining"`
}

// knownQuotas maps snapshot keys to display labels, in display order. Labels
// stay within render's label column. Any other snapshot key is passed through
// as an unknown meter so server-side additions are never silently dropped.
var knownQuotas = []struct{ key, label string }{
	{"premium_interactions", "Premium"},
	{"chat", "Chat"},
	{"completions", "Completions"},
}

// parseUsage converts a copilot_internal payload into the normalized model. It
// is a pure function over bytes to keep it unit-testable.
func parseUsage(body []byte) (*usage.Usage, error) {
	var r copilotResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("copilot usage を解析できません: %w", err)
	}

	u := &usage.Usage{
		Provider: "copilot",
		Account:  r.Login,
		Plan:     r.CopilotPlan,
	}

	// All Copilot quotas reset together on a monthly cadence; the window opened
	// one calendar month before the reset (resets fall on the 1st at 00:00 UTC).
	resetsAt := parseResetTime(r.QuotaResetDateUTC, r.QuotaResetDate)
	var windowStart *time.Time
	if resetsAt != nil {
		windowStart = usage.Ptr(resetsAt.AddDate(0, -1, 0))
	}

	seen := map[string]bool{}
	knownFound := 0
	add := func(key, label string, snap quotaSnapshot, known bool) {
		seen[key] = true
		u.Meters = append(u.Meters, buildMeter(key, label, snap, known, resetsAt, windowStart))
		if known {
			knownFound++
		}
	}

	for _, kq := range knownQuotas {
		seen[kq.key] = true
		if snap, ok := r.QuotaSnapshots[kq.key]; ok {
			add(kq.key, kq.label, snap, true)
		}
	}

	// Pass through any remaining snapshot keys (sorted for stability).
	var rest []string
	for k := range r.QuotaSnapshots {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		add(k, k, r.QuotaSnapshots[k], false)
	}

	if knownFound == 0 {
		return nil, fmt.Errorf("copilot usage に既知の枠が見つかりません（API 仕様変更の可能性）")
	}
	return u, nil
}

// buildMeter normalizes one quota snapshot. Unlimited quotas become a marker
// line (label suffixed, no bar); metered quotas carry percent + counts + reset.
func buildMeter(key, label string, s quotaSnapshot, known bool, resetsAt, windowStart *time.Time) usage.Meter {
	if s.Unlimited {
		return usage.Meter{
			Key:   key,
			Label: label + " (unlimited)",
			Unit:  usage.UnitRequests,
			Known: known,
		}
	}

	m := usage.Meter{Key: key, Label: label, Unit: usage.UnitRequests, Known: known}

	if s.Entitlement != nil && *s.Entitlement > 0 {
		m.Limit = s.Entitlement
	}
	// quota_remaining is the precise float; remaining is its rounded sibling.
	if s.QuotaRemaining != nil {
		m.Remaining = s.QuotaRemaining
	} else if s.Remaining != nil {
		m.Remaining = s.Remaining
	}
	if m.Limit != nil && m.Remaining != nil {
		m.Used = usage.Ptr(*m.Limit - *m.Remaining)
	}

	switch {
	case s.PercentRemaining != nil:
		used := 100 - *s.PercentRemaining
		if used < 0 {
			used = 0
		}
		m.UsedPercent = usage.Ptr(used)
	case m.Limit != nil && m.Remaining != nil:
		m.UsedPercent = usage.Ptr((*m.Limit - *m.Remaining) / *m.Limit * 100)
	}

	if resetsAt != nil {
		m.ResetsAt = resetsAt
		m.WindowStart = windowStart
	}
	return m
}

// parseResetTime prefers the RFC3339 UTC timestamp, falling back to the
// date-only field ("2026-06-01", interpreted as midnight UTC).
func parseResetTime(utc, date string) *time.Time {
	if utc != "" {
		if t, err := time.Parse(time.RFC3339, utc); err == nil {
			return &t
		}
	}
	if date != "" {
		if t, err := time.Parse("2006-01-02", date); err == nil {
			return &t
		}
	}
	return nil
}
