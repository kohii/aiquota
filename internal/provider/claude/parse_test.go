package claude

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

func TestParseUsage_RealShape(t *testing.T) {
	body := []byte(`{
		"five_hour":  {"utilization": 9.0,  "resets_at": "2026-05-31T01:59:59.726121+00:00"},
		"seven_day":  {"utilization": 16.0, "resets_at": "2026-06-02T13:00:00.726142+00:00"},
		"seven_day_oauth_apps": null,
		"seven_day_opus": null,
		"seven_day_sonnet": {"utilization": 3.0, "resets_at": "2026-06-02T13:00:00.726150+00:00"},
		"seven_day_cowork": null,
		"future_window": {"utilization": 42.0, "resets_at": "2026-06-02T13:00:00Z"},
		"extra_usage": {"is_enabled": true, "monthly_limit": 10000, "used_credits": 32.0, "utilization": 0.32, "currency": "USD"}
	}`)

	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}

	five := findMeter(u, "five_hour")
	if five == nil || five.UsedPercent == nil || *five.UsedPercent != 9 || !five.Known {
		t.Errorf("five_hour wrong: %+v", five)
	}
	if five.ResetsAt == nil {
		t.Errorf("five_hour missing ResetsAt")
	}
	if findMeter(u, "seven_day_sonnet") == nil {
		t.Errorf("seven_day_sonnet missing")
	}
	// null windows must be skipped.
	if findMeter(u, "seven_day_opus") != nil {
		t.Errorf("seven_day_opus should be skipped (null)")
	}

	// Unknown window passed through with Known=false.
	fut := findMeter(u, "future_window")
	if fut == nil || fut.Known {
		t.Errorf("future_window should be unknown meter: %+v", fut)
	}

	// extra_usage normalized from cents to USD.
	extra := findMeter(u, "extra_usage")
	if extra == nil || extra.Unit != usage.UnitUSD {
		t.Fatalf("extra_usage wrong: %+v", extra)
	}
	if extra.Used == nil || *extra.Used != 0.32 {
		t.Errorf("extra used = %v, want 0.32 (32 cents)", extra.Used)
	}
	if extra.Limit == nil || *extra.Limit != 100 {
		t.Errorf("extra limit = %v, want 100 (10000 cents)", extra.Limit)
	}
}

func TestParseUsage_ExtraUsageDisabled(t *testing.T) {
	body := []byte(`{
		"five_hour": {"utilization": 1.0, "resets_at": "2026-05-31T01:59:59Z"},
		"extra_usage": {"is_enabled": false, "monthly_limit": 10000, "used_credits": 0, "currency": "USD"}
	}`)
	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	if findMeter(u, "extra_usage") != nil {
		t.Errorf("disabled extra_usage should be omitted")
	}
}
