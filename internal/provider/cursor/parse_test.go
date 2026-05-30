package cursor

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

func TestParseUsage_ProIndividual(t *testing.T) {
	body := []byte(`{
		"billingCycleStart": "2026-05-27T05:01:06.000Z",
		"billingCycleEnd": "2026-06-27T05:01:06.000Z",
		"membershipType": "pro",
		"isUnlimited": false,
		"individualUsage": {
			"plan": {
				"used": 2000, "limit": 2000, "remaining": 0,
				"breakdown": {"included": 2000, "bonus": 2000, "total": 4000},
				"autoPercentUsed": 4.1, "apiPercentUsed": 75.7, "totalPercentUsed": 25
			},
			"onDemand": {"enabled": false, "used": 0, "limit": null, "remaining": null}
		},
		"teamUsage": {}
	}`)

	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	if u.Plan != "pro" {
		t.Errorf("plan = %q", u.Plan)
	}

	plan := findMeter(u, "plan")
	if plan == nil {
		t.Fatal("plan meter missing")
	}
	// USD derived from breakdown.total ($40), not included-only used/limit.
	if plan.Limit == nil || *plan.Limit != 40 {
		t.Errorf("plan.Limit = %v, want 40 (breakdown.total 4000 cents)", plan.Limit)
	}
	if plan.Used == nil || *plan.Used != 10 { // 25% of $40
		t.Errorf("plan.Used = %v, want 10 (25%% of $40)", plan.Used)
	}
	if plan.Remaining == nil || *plan.Remaining != 30 {
		t.Errorf("plan.Remaining = %v, want 30", plan.Remaining)
	}
	if plan.UsedPercent == nil || *plan.UsedPercent != 25 {
		t.Errorf("plan.UsedPercent = %v", plan.UsedPercent)
	}
	if plan.ResetsAt == nil {
		t.Errorf("plan.ResetsAt should come from billingCycleEnd")
	}
	if findMeter(u, "plan_auto") == nil || findMeter(u, "plan_api") == nil {
		t.Errorf("auto/api breakdown meters missing")
	}
	// onDemand disabled -> no meter.
	if findMeter(u, "on_demand") != nil {
		t.Errorf("disabled on_demand should be omitted")
	}
}

func TestParseUsage_OnDemandEnabled(t *testing.T) {
	body := []byte(`{
		"membershipType": "pro",
		"individualUsage": {
			"plan": {"used": 0, "limit": 2000, "totalPercentUsed": 0},
			"onDemand": {"enabled": true, "used": 550, "limit": 5000, "remaining": 4450}
		}
	}`)
	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	od := findMeter(u, "on_demand")
	if od == nil || od.Unit != usage.UnitUSD {
		t.Fatalf("on_demand wrong: %+v", od)
	}
	if od.Used == nil || *od.Used != 5.5 {
		t.Errorf("on_demand used = %v, want 5.5", od.Used)
	}
}

func TestParseUsage_TeamPooled(t *testing.T) {
	body := []byte(`{
		"membershipType": "team",
		"individualUsage": {
			"plan": {"totalPercentUsed": 0},
			"onDemand": {"enabled": false}
		},
		"teamUsage": {
			"pooled":   {"enabled": true, "used": 12345, "limit": 50000, "remaining": 37655},
			"onDemand": {"enabled": false}
		}
	}`)
	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	pooled := findMeter(u, "team_pooled")
	if pooled == nil || pooled.Used == nil || *pooled.Used != 123.45 {
		t.Fatalf("team_pooled wrong: %+v", pooled)
	}
	if findMeter(u, "team_on_demand") != nil {
		t.Errorf("disabled team on_demand should be omitted")
	}
}

func TestParseUsage_EmptyIsDrift(t *testing.T) {
	// No plan data, not unlimited, no team usage -> error rather than empty success.
	body := []byte(`{"membershipType":"pro","individualUsage":{"plan":{},"onDemand":{"enabled":false}},"teamUsage":{}}`)
	if _, err := parseUsage(body); err == nil {
		t.Fatal("expected error for empty usage payload")
	}
}

func TestParseUsage_Unlimited(t *testing.T) {
	body := []byte(`{"membershipType":"enterprise","isUnlimited":true,"individualUsage":{"plan":{},"onDemand":{"enabled":false}},"teamUsage":{}}`)
	u, err := parseUsage(body)
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	if m := findMeter(u, "plan"); m == nil {
		t.Errorf("unlimited plan should yield a marker meter")
	}
}

func TestJWTSubject(t *testing.T) {
	// header.payload.sig where payload = {"sub":"github|123"}
	tok := "x.eyJzdWIiOiJnaXRodWJ8MTIzIn0.y"
	sub, err := jwtSubject(tok)
	if err != nil {
		t.Fatalf("jwtSubject: %v", err)
	}
	if sub != "github|123" {
		t.Errorf("sub = %q, want github|123", sub)
	}
}
