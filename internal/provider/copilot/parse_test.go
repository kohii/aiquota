package copilot

import (
	"math"
	"testing"
	"time"

	"github.com/kohii/aiquota/internal/usage"
)

func findMeter(u *usage.Usage, key string) *usage.Meter {
	for i := range u.Meters {
		if u.Meters[i].Key == key {
			return &u.Meters[i]
		}
	}
	return nil
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// Real-shaped business-plan payload: premium metered, chat/completions unlimited.
func TestParseUsage_BusinessPlan(t *testing.T) {
	body := []byte(`{
		"login": "kohii",
		"copilot_plan": "business",
		"quota_reset_date": "2026-06-01",
		"quota_reset_date_utc": "2026-06-01T00:00:00.000Z",
		"quota_snapshots": {
			"chat":         {"quota_id":"chat","unlimited":true,"percent_remaining":100.0,"remaining":0,"entitlement":0},
			"completions":  {"quota_id":"completions","unlimited":true,"percent_remaining":100.0,"remaining":0,"entitlement":0},
			"premium_interactions": {"quota_id":"premium_interactions","unlimited":false,"percent_remaining":96.1,"quota_remaining":288.4,"remaining":288,"entitlement":300}
		}
	}`)

	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	if u.Account != "kohii" || u.Plan != "business" {
		t.Errorf("account/plan = %q/%q", u.Account, u.Plan)
	}
	if got := len(u.Meters); got != 3 {
		t.Fatalf("meters = %d, want 3", got)
	}

	prem := findMeter(u, "premium_interactions")
	if prem == nil || prem.UsedPercent == nil || !approx(*prem.UsedPercent, 3.9) {
		t.Errorf("premium UsedPercent wrong: %+v", prem)
	}
	if prem.Limit == nil || *prem.Limit != 300 {
		t.Errorf("premium Limit = %v, want 300", prem.Limit)
	}
	if prem.Remaining == nil || !approx(*prem.Remaining, 288.4) {
		t.Errorf("premium Remaining should prefer quota_remaining: %v", prem.Remaining)
	}
	if prem.Used == nil || !approx(*prem.Used, 11.6) {
		t.Errorf("premium Used = %v, want ~11.6", prem.Used)
	}
	// Monthly window: reset Jun 1 => window opened May 1.
	if prem.ResetsAt == nil || prem.WindowStart == nil {
		t.Fatalf("premium missing window bounds: %+v", prem)
	}
	wantStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if !prem.WindowStart.Equal(wantStart) {
		t.Errorf("premium WindowStart = %v, want %v", prem.WindowStart.UTC(), wantStart)
	}

	// Unlimited quotas become marker lines: no percent, label suffixed.
	chat := findMeter(u, "chat")
	if chat == nil || chat.UsedPercent != nil {
		t.Errorf("chat should be an unlimited marker (no percent): %+v", chat)
	}
	if chat.Label != "Chat (unlimited)" {
		t.Errorf("chat label = %q", chat.Label)
	}
	// Unlimited markers carry no window, so the renderer prints no stray "resets".
	if chat.ResetsAt != nil || chat.WindowStart != nil {
		t.Errorf("unlimited marker should have no window bounds: %+v", chat)
	}
}

// Free-style plan where chat/completions are metered and percent_remaining is
// absent, so the percentage must be derived from entitlement/remaining.
func TestParseUsage_DerivedPercent(t *testing.T) {
	body := []byte(`{
		"login": "u",
		"copilot_plan": "free",
		"quota_reset_date_utc": "2026-06-01T00:00:00Z",
		"quota_snapshots": {
			"chat":        {"quota_id":"chat","unlimited":false,"entitlement":50,"remaining":10},
			"completions": {"quota_id":"completions","unlimited":false,"entitlement":2000,"remaining":500}
		}
	}`)

	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	chat := findMeter(u, "chat")
	if chat == nil || chat.UsedPercent == nil || !approx(*chat.UsedPercent, 80) {
		t.Errorf("chat derived percent wrong: %+v", chat)
	}
}

func TestParseUsage_UnknownQuotaPassthrough(t *testing.T) {
	body := []byte(`{
		"copilot_plan": "pro",
		"quota_reset_date_utc": "2026-06-01T00:00:00Z",
		"quota_snapshots": {
			"premium_interactions": {"quota_id":"premium_interactions","unlimited":false,"percent_remaining":50,"entitlement":300,"remaining":150},
			"experimental_agent": {"quota_id":"experimental_agent","unlimited":false,"percent_remaining":10,"entitlement":100,"remaining":10}
		}
	}`)

	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	x := findMeter(u, "experimental_agent")
	if x == nil || x.Known {
		t.Errorf("unknown quota should be passed through with Known=false: %+v", x)
	}
}

func TestParseUsage_DriftReturnsError(t *testing.T) {
	// No recognizable quotas -> schema drift -> error (not a silent success).
	body := []byte(`{"copilot_plan":"pro","quota_snapshots":{}}`)
	if _, err := parseUsage(body); err == nil {
		t.Fatal("expected error when no known quotas are present")
	}
}
