package codex

import (
	"testing"

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

func TestParseUsage_PlusAccount(t *testing.T) {
	body := []byte(`{
		"email": "u@example.com",
		"plan_type": "plus",
		"rate_limit": {
			"primary_window":   {"used_percent": 26, "limit_window_seconds": 18000, "reset_at": 1780193222},
			"secondary_window": {"used_percent": 98, "limit_window_seconds": 604800, "reset_at": 1780187148}
		},
		"additional_rate_limits": null,
		"credits": {"has_credits": false, "unlimited": false, "balance": "0"}
	}`)

	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	if u.Account != "u@example.com" || u.Plan != "plus" {
		t.Errorf("account/plan = %q/%q", u.Account, u.Plan)
	}
	if got := len(u.Meters); got != 2 {
		t.Fatalf("meters = %d, want 2 (no credits when balance 0)", got)
	}

	five := findMeter(u, "5h")
	if five == nil || five.UsedPercent == nil || *five.UsedPercent != 26 {
		t.Errorf("5h meter wrong: %+v", five)
	}
	if five.ResetsAt == nil {
		t.Errorf("5h meter missing ResetsAt")
	}
	week := findMeter(u, "weekly")
	if week == nil || week.UsedPercent == nil || *week.UsedPercent != 98 {
		t.Errorf("weekly meter wrong: %+v", week)
	}
}

func TestParseUsage_WithCreditsAndExtraWindow(t *testing.T) {
	body := []byte(`{
		"plan_type": "pro",
		"rate_limit": {
			"primary_window": {"used_percent": 10, "limit_window_seconds": 18000, "reset_at": 0}
		},
		"additional_rate_limits": [
			{"name": "spark", "window": {"used_percent": 50, "limit_window_seconds": 18000, "reset_at": 0}}
		],
		"credits": {"has_credits": true, "unlimited": false, "balance": "12.5"}
	}`)

	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	spark := findMeter(u, "spark")
	if spark == nil || spark.Known {
		t.Errorf("spark should be an unknown meter: %+v", spark)
	}
	credits := findMeter(u, "credits")
	if credits == nil || credits.Used == nil || *credits.Used != 12.5 {
		t.Errorf("credits meter wrong: %+v", credits)
	}
	// reset_at == 0 must not produce a ResetsAt.
	if five := findMeter(u, "5h"); five == nil || five.ResetsAt != nil {
		t.Errorf("5h ResetsAt should be nil for reset_at=0: %+v", five)
	}
}

func TestParseUsage_DriftReturnsError(t *testing.T) {
	// No recognizable windows -> schema drift -> error (not a silent success).
	body := []byte(`{"plan_type":"pro","rate_limit":{},"credits":{}}`)
	if _, err := parseUsage(body); err == nil {
		t.Fatal("expected error when no known windows are present")
	}
}

func TestParseUsage_UnlimitedCredits(t *testing.T) {
	body := []byte(`{
		"plan_type":"pro",
		"rate_limit":{"primary_window":{"used_percent":5,"limit_window_seconds":18000,"reset_at":0}},
		"credits":{"unlimited":true,"balance":"0"}
	}`)
	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	c := findMeter(u, "credits")
	if c == nil || c.Used != nil {
		t.Errorf("unlimited credits should be a marker meter with no Used: %+v", c)
	}
}
