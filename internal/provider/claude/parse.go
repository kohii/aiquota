package claude

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/kohii/aiquota/internal/usage"
)

// window is the {utilization, resets_at} shape used by every rate window in
// the OAuth usage payload.
type window struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

// knownWindows maps payload keys to display labels and window lengths, in
// display order. The API reports only resets_at, so the window length is used
// to derive WindowStart for the pace marker. Any other window-shaped key is
// passed through as an unknown meter (no length → no pace marker) so
// server-side additions (new models, promos) are never dropped.
var knownWindows = []struct {
	key, label string
	length     time.Duration
}{
	{"five_hour", "5h limit", 5 * time.Hour},
	{"seven_day", "Weekly", 7 * 24 * time.Hour},
	{"seven_day_opus", "Weekly (Opus)", 7 * 24 * time.Hour},
	{"seven_day_sonnet", "Weekly (Sonnet)", 7 * 24 * time.Hour},
}

// parseUsage converts an OAuth usage payload into the normalized model.
func parseUsage(body []byte) (*usage.Usage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("claude usage を解析できません: %w", err)
	}

	u := &usage.Usage{Provider: "claude"}
	seen := map[string]bool{}
	knownFound := 0

	addWindow := func(key, label string, length time.Duration, known bool) {
		seen[key] = true
		rawMsg, ok := raw[key]
		if !ok {
			return
		}
		var w window
		if err := json.Unmarshal(rawMsg, &w); err != nil || w.Utilization == nil {
			return // null or not a window
		}
		m := usage.Meter{
			Key:         key,
			Label:       label,
			UsedPercent: w.Utilization,
			Unit:        usage.UnitPercent,
			Known:       known,
		}
		if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
			m.ResetsAt = &t
			if length > 0 {
				m.WindowStart = usage.Ptr(t.Add(-length))
			}
		}
		u.Meters = append(u.Meters, m)
		if known {
			knownFound++
		}
	}

	for _, kw := range knownWindows {
		addWindow(kw.key, kw.label, kw.length, true)
	}

	// extra_usage is a cost meter, not a window.
	seen["extra_usage"] = true
	if rawMsg, ok := raw["extra_usage"]; ok {
		var e struct {
			IsEnabled    bool     `json:"is_enabled"`
			MonthlyLimit *float64 `json:"monthly_limit"`
			UsedCredits  *float64 `json:"used_credits"`
			Utilization  *float64 `json:"utilization"`
			Currency     string   `json:"currency"`
		}
		if err := json.Unmarshal(rawMsg, &e); err == nil && e.IsEnabled {
			m := usage.Meter{
				Key:         "extra_usage",
				Label:       "Extra usage",
				UsedPercent: e.Utilization,
				Unit:        usage.UnitUSD,
				Currency:    e.Currency,
				Known:       true,
			}
			// API reports cents; normalize to currency units.
			if e.UsedCredits != nil {
				m.Used = usage.Ptr(*e.UsedCredits / 100)
			}
			if e.MonthlyLimit != nil {
				m.Limit = usage.Ptr(*e.MonthlyLimit / 100)
			}
			u.Meters = append(u.Meters, m)
			knownFound++
		}
	}

	// Pass through any remaining window-shaped keys (sorted for stability).
	var rest []string
	for k := range raw {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		addWindow(k, k, 0, false)
	}

	if knownFound == 0 {
		return nil, fmt.Errorf("claude usage に既知の枠が見つかりません（API 仕様変更の可能性）")
	}
	return u, nil
}
