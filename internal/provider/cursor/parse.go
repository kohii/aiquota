package cursor

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kohii/aiquota/internal/usage"
)

// usageSummary mirrors GET /api/usage-summary. Monetary fields are in cents.
type usageSummary struct {
	BillingCycleStart string `json:"billingCycleStart"`
	BillingCycleEnd   string `json:"billingCycleEnd"`
	MembershipType    string `json:"membershipType"`
	IsUnlimited       bool   `json:"isUnlimited"`
	IndividualUsage   struct {
		Plan struct {
			Used             *float64 `json:"used"`
			Limit            *float64 `json:"limit"`
			Remaining        *float64 `json:"remaining"`
			AutoPercentUsed  *float64 `json:"autoPercentUsed"`
			APIPercentUsed   *float64 `json:"apiPercentUsed"`
			TotalPercentUsed *float64 `json:"totalPercentUsed"`
			Breakdown        struct {
				Included *float64 `json:"included"`
				Bonus    *float64 `json:"bonus"`
				Total    *float64 `json:"total"`
			} `json:"breakdown"`
		} `json:"plan"`
		OnDemand onDemand `json:"onDemand"`
	} `json:"individualUsage"`
	TeamUsage struct {
		Pooled   *onDemand `json:"pooled"`
		OnDemand *onDemand `json:"onDemand"`
	} `json:"teamUsage"`
}

type onDemand struct {
	Enabled   bool     `json:"enabled"`
	Used      *float64 `json:"used"`
	Limit     *float64 `json:"limit"`
	Remaining *float64 `json:"remaining"`
}

// parseUsage converts a usage-summary payload into the normalized model.
func parseUsage(body []byte) (*usage.Usage, error) {
	var r usageSummary
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("cursor usage を解析できません: %w", err)
	}

	u := &usage.Usage{Provider: "cursor", Plan: r.MembershipType}

	var resetsAt, windowStart *time.Time
	if t, err := time.Parse(time.RFC3339, r.BillingCycleEnd); err == nil {
		resetsAt = &t
	}
	if t, err := time.Parse(time.RFC3339, r.BillingCycleStart); err == nil {
		windowStart = &t
	}

	plan := r.IndividualUsage.Plan
	// Total plan spend (USD), the headline meter. totalPercentUsed is measured
	// against the full available allowance (breakdown.total = included + bonus),
	// not the included-only plan.used/limit — so derive USD from breakdown.total
	// to keep the percent and the dollars consistent. Fall back to plan.used/
	// limit when no breakdown is present.
	total := usage.Meter{
		Key:         "plan",
		Label:       "Plan (total)",
		UsedPercent: plan.TotalPercentUsed,
		Unit:        usage.UnitUSD,
		Currency:    "USD",
		ResetsAt:    resetsAt,
		WindowStart: windowStart,
		Known:       true,
	}
	if limit := centsToUSD(plan.Breakdown.Total); limit != nil {
		total.Limit = limit
		if plan.TotalPercentUsed != nil {
			used := *limit * *plan.TotalPercentUsed / 100
			total.Used = usage.Ptr(used)
			total.Remaining = usage.Ptr(*limit - used)
		}
	} else {
		total.Used = centsToUSD(plan.Used)
		total.Limit = centsToUSD(plan.Limit)
		total.Remaining = centsToUSD(plan.Remaining)
	}
	// Only emit the plan meter when it actually carries data.
	if total.UsedPercent != nil || total.Used != nil || total.Limit != nil {
		u.Meters = append(u.Meters, total)
	} else if r.IsUnlimited {
		u.Meters = append(u.Meters, usage.Meter{
			Key: "plan", Label: "Plan (unlimited)", ResetsAt: resetsAt, WindowStart: windowStart, Known: true,
		})
	}

	// Auto / API breakdown (percent only). They run over the same billing cycle,
	// so they carry the same window for the pace marker.
	if plan.AutoPercentUsed != nil {
		u.Meters = append(u.Meters, usage.Meter{
			Key: "plan_auto", Label: "Auto models", UsedPercent: plan.AutoPercentUsed,
			Unit: usage.UnitPercent, ResetsAt: resetsAt, WindowStart: windowStart, Known: true,
		})
	}
	if plan.APIPercentUsed != nil {
		u.Meters = append(u.Meters, usage.Meter{
			Key: "plan_api", Label: "Named/API models", UsedPercent: plan.APIPercentUsed,
			Unit: usage.UnitPercent, ResetsAt: resetsAt, WindowStart: windowStart, Known: true,
		})
	}

	// On-demand (overage) spend, only when enabled.
	u.Meters = appendSpend(u.Meters, "on_demand", "On-demand", &r.IndividualUsage.OnDemand)
	// Team plan: shared pooled allowance and team-level on-demand.
	u.Meters = appendSpend(u.Meters, "team_pooled", "Team pooled", r.TeamUsage.Pooled)
	u.Meters = appendSpend(u.Meters, "team_on_demand", "Team on-demand", r.TeamUsage.OnDemand)

	if len(u.Meters) == 0 {
		return nil, fmt.Errorf("cursor usage に既知の枠が見つかりません（API 仕様変更の可能性）")
	}
	return u, nil
}

// appendSpend adds a USD spend meter when od is present and enabled.
func appendSpend(meters []usage.Meter, key, label string, od *onDemand) []usage.Meter {
	if od == nil || !od.Enabled {
		return meters
	}
	return append(meters, usage.Meter{
		Key: key, Label: label,
		Used: centsToUSD(od.Used), Limit: centsToUSD(od.Limit), Remaining: centsToUSD(od.Remaining),
		Unit: usage.UnitUSD, Currency: "USD", Known: true,
	})
}

func centsToUSD(cents *float64) *float64 {
	if cents == nil {
		return nil
	}
	return usage.Ptr(*cents / 100)
}
