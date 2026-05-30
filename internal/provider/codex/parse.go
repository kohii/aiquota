package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kohii/aiquota/internal/usage"
)

// whamResponse mirrors the fields of GET /backend-api/wham/usage that we use.
type whamResponse struct {
	Email     string `json:"email"`
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		PrimaryWindow   *whamWindow `json:"primary_window"`
		SecondaryWindow *whamWindow `json:"secondary_window"`
	} `json:"rate_limit"`
	AdditionalRateLimits []whamAdditional `json:"additional_rate_limits"`
	Credits              struct {
		HasCredits bool   `json:"has_credits"`
		Unlimited  bool   `json:"unlimited"`
		Balance    string `json:"balance"`
	} `json:"credits"`
}

type whamWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

// whamAdditional captures model-specific extra windows (e.g. Spark). Field
// names are a best-effort guess; unknown windows are passed through anyway.
type whamAdditional struct {
	Name   string      `json:"name"`
	Window *whamWindow `json:"window"`
}

// parseUsage converts a wham usage payload into the normalized model. It is a
// pure function over bytes to keep it unit-testable.
func parseUsage(body []byte) (*usage.Usage, error) {
	var r whamResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("codex usage を解析できません: %w", err)
	}

	u := &usage.Usage{
		Provider: "codex",
		Account:  r.Email,
		Plan:     r.PlanType,
	}

	knownFound := 0
	if m, ok := windowMeter("5h", "5h limit", r.RateLimit.PrimaryWindow, true); ok {
		u.Meters = append(u.Meters, m)
		knownFound++
	}
	if m, ok := windowMeter("weekly", "Weekly limit", r.RateLimit.SecondaryWindow, true); ok {
		u.Meters = append(u.Meters, m)
		knownFound++
	}
	for _, a := range r.AdditionalRateLimits {
		label := a.Name
		if label == "" {
			label = "Extra limit"
		}
		if m, ok := windowMeter(a.Name, label, a.Window, false); ok {
			u.Meters = append(u.Meters, m)
		}
	}

	// Credits: surface as an unlimited marker, or a balance when the account
	// actually has paid credits.
	switch {
	case r.Credits.Unlimited:
		u.Meters = append(u.Meters, usage.Meter{
			Key: "credits", Label: "Credits (unlimited)", Unit: usage.UnitCredits, Known: true,
		})
	case r.Credits.HasCredits || (r.Credits.Balance != "" && r.Credits.Balance != "0"):
		if bal, ok := parseFloat(r.Credits.Balance); ok {
			u.Meters = append(u.Meters, usage.Meter{
				Key: "credits", Label: "Credits", Used: &bal, Unit: usage.UnitCredits, Known: true,
			})
		}
	}

	if knownFound == 0 {
		return nil, errors.New("codex usage に既知の枠が見つかりません（API 仕様変更の可能性）")
	}
	return u, nil
}

func windowMeter(key, label string, w *whamWindow, known bool) (usage.Meter, bool) {
	if w == nil {
		return usage.Meter{}, false
	}
	m := usage.Meter{
		Key:         key,
		Label:       label,
		UsedPercent: usage.Ptr(w.UsedPercent),
		Unit:        usage.UnitPercent,
		Known:       known,
	}
	if w.ResetAt > 0 {
		m.ResetsAt = usage.Ptr(time.Unix(w.ResetAt, 0))
		// The window opened limit_window_seconds before it resets.
		if w.LimitWindowSeconds > 0 {
			m.WindowStart = usage.Ptr(time.Unix(w.ResetAt-w.LimitWindowSeconds, 0))
		}
	}
	return m, true
}

// parseFloat parses a numeric credits balance; ok is false for non-numeric
// values (e.g. "unlimited"), so callers can skip emitting a bogus 0.
func parseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
