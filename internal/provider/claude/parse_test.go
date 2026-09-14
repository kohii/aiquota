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
		"nimbus_quill": {"utilization": 0.0, "resets_at": "2026-06-02T13:00:00Z"},
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
	if findMeter(u, "nimbus_quill") != nil {
		t.Errorf("nimbus_quill should be omitted")
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

// TestParseUsage_WeeklyScopedLimit covers the newer `limits` array shape,
// where a per-model weekly quota (e.g. Fable rotating in for the retired
// seven_day_sonnet key) is surfaced via a "weekly_scoped" entry instead of a
// flat seven_day_<model> key.
func TestParseUsage_WeeklyScopedLimit(t *testing.T) {
	body := []byte(`{
		"five_hour": {"utilization": 63.0, "resets_at": "2026-07-03T03:09:59Z"},
		"seven_day": {"utilization": 44.0, "resets_at": "2026-07-03T21:59:59Z"},
		"seven_day_opus": null,
		"seven_day_sonnet": null,
		"limits": [
			{"kind": "session", "group": "session", "percent": 63, "resets_at": "2026-07-03T03:09:59Z", "scope": null},
			{"kind": "weekly_all", "group": "weekly", "percent": 44, "resets_at": "2026-07-03T21:59:59Z", "scope": null},
			{"kind": "weekly_scoped", "group": "weekly", "percent": 42, "resets_at": "2026-07-03T21:59:59Z", "scope": {"model": {"id": null, "display_name": "Fable"}}}
		]
	}`)

	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}

	fable := findMeter(u, "weekly_scoped_fable")
	if fable == nil {
		t.Fatalf("weekly_scoped_fable meter missing")
	}
	if !fable.Known || fable.Label != "Weekly (Fable)" {
		t.Errorf("fable meter wrong: %+v", fable)
	}
	if fable.UsedPercent == nil || *fable.UsedPercent != 42 {
		t.Errorf("fable usedPercent = %v, want 42", fable.UsedPercent)
	}
	if fable.WindowStart == nil {
		t.Errorf("fable meter missing WindowStart")
	}

	// session/weekly_all duplicate five_hour/seven_day and must not appear
	// as separate meters.
	for _, key := range []string{"limits[0]_session", "limits[1]_weekly_all"} {
		if findMeter(u, key) != nil {
			t.Errorf("%s should have been skipped as a duplicate", key)
		}
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
