package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/kohii/aiquota/internal/usage"
)

func TestRenderBar_Bounds(t *testing.T) {
	p := palette{on: false}
	cases := []struct {
		pct        float64
		wantFilled int
	}{
		{0, 0},
		{100, barWidth},
		{150, barWidth}, // clamp above 100
		{-5, 0},         // clamp below 0
		{50, barWidth / 2},
	}
	for _, c := range cases {
		bar := renderBar(p, levelGood, c.pct, nil)
		filled := strings.Count(bar, "█")
		if filled != c.wantFilled {
			t.Errorf("pct %.0f: filled=%d want %d (bar=%q)", c.pct, filled, c.wantFilled, bar)
		}
		if total := strings.Count(bar, "█") + strings.Count(bar, "░"); total != barWidth {
			t.Errorf("pct %.0f: total cells=%d want %d", c.pct, total, barWidth)
		}
	}
}

func TestPacePercent(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reset := start.Add(10 * time.Hour)
	cases := []struct {
		now  time.Time
		want float64
	}{
		{start, 0},
		{start.Add(5 * time.Hour), 50},
		{start.Add(10 * time.Hour), 100},
		{start.Add(-time.Hour), 0},       // clamp below
		{start.Add(20 * time.Hour), 100}, // clamp above
	}
	for _, c := range cases {
		if got := pacePercent(start, reset, c.now); got != c.want {
			t.Errorf("pacePercent(now=%v) = %v, want %v", c.now, got, c.want)
		}
	}
	// Degenerate window.
	if got := pacePercent(reset, start, start); got != 0 {
		t.Errorf("non-positive window => %v, want 0", got)
	}
}

func TestLevelOf(t *testing.T) {
	cases := []struct {
		name string
		pct  float64
		pace *float64
		want level
	}{
		// No usable pace → fall back to absolute usage (never "loss").
		{"no-pace low", 20, nil, levelGood},
		{"no-pace mid", 70, nil, levelWarn},
		{"no-pace high", 95, nil, levelCrit},
		{"pace-zero falls back", 70, ptr(0.0), levelWarn},
		// Mature pace projects end-of-window usage.
		{"loss far behind", 18, ptr(75.0), levelLoss},         // proj 24%
		{"used nothing late", 0, ptr(80.0), levelLoss},        // proj 0%
		{"on track", 48, ptr(50.0), levelGood},                // proj 96%
		{"high but on pace", 65, ptr(90.0), levelGood},        // proj 72% — not a warn
		{"window over, half used", 50, ptr(100.0), levelLoss}, // proj 50%
		{"slightly ahead", 33, ptr(28.0), levelWarn},          // proj ~118%
		{"burning hot", 50, ptr(24.0), levelCrit},             // proj 208% — P0 case, not green
		{"near cap always crit", 90, ptr(55.0), levelCrit},
		// Early window: too soon to judge by pace; absolute nets still apply.
		{"early modest stays calm", 50, ptr(19.0), levelGood},
		{"early high warns", 70, ptr(10.0), levelWarn},
		{"loss waits past lossFloor", 5, ptr(22.0), levelGood}, // 22<25 → not loss yet
		{"loss once past lossFloor", 5, ptr(26.0), levelLoss},
	}
	for _, c := range cases {
		if got := levelOf(c.pct, c.pace); got != c.want {
			t.Errorf("%s: levelOf(%.0f, %v) = %d, want %d", c.name, c.pct, c.pace, got, c.want)
		}
	}
}

func TestRenderBar_Marker(t *testing.T) {
	p := palette{on: false}
	// 30% used, pace 50%: marker sits at cell 8 (round(0.5*16)).
	bar := renderBar(p, levelGood, 30, ptr(50.0))
	if !strings.Contains(bar, "│") {
		t.Fatalf("expected pace marker in bar: %q", bar)
	}
	// Marker replaces exactly one cell; total visible cells stay barWidth.
	cells := strings.Count(bar, "█") + strings.Count(bar, "░") + strings.Count(bar, "│")
	if cells != barWidth {
		t.Errorf("cells = %d, want %d (bar=%q)", cells, barWidth, bar)
	}
	// 30% used (≈5 filled) is left of the 50% marker (cell 8): headroom case,
	// so the marker should fall on an empty cell, i.e. preceded by some ░.
	idx := strings.Index(bar, "│")
	if idx <= 0 || !strings.ContainsRune(bar[:idx], '░') {
		t.Errorf("expected ░ before marker when under pace: %q", bar)
	}
}

func TestHumanReset(t *testing.T) {
	now := time.Now()
	// +2s buffer so the floor in humanReset doesn't drop a unit due to the few
	// microseconds elapsed between Add and the internal time.Until.
	if got := humanReset(now.Add(-time.Hour)); got != "now" {
		t.Errorf("past => %q, want now", got)
	}
	if got := humanReset(now.Add(90*time.Minute + 2*time.Second)); got != "1h30m" {
		t.Errorf("90m => %q, want 1h30m", got)
	}
	if got := humanReset(now.Add(40*time.Minute + 2*time.Second)); got != "40m" {
		t.Errorf("40m => %q, want 40m", got)
	}
	if got := humanReset(now.Add(26*time.Hour + 2*time.Second)); got != "1d2h" {
		t.Errorf("26h => %q, want 1d2h", got)
	}
	// Beyond 48h falls back to an absolute date (just assert it is not relative).
	if got := humanReset(now.Add(72 * time.Hour)); strings.Contains(got, "d") && !strings.ContainsAny(got, " ") {
		t.Errorf("72h => %q, expected absolute date", got)
	}
}

func TestPadRight_Multibyte(t *testing.T) {
	// 3 runes; padding should be rune-aware so columns align.
	got := padRight("週次枠", 6)
	if want := "週次枠" + "   "; got != want {
		t.Errorf("padRight multibyte = %q, want %q", got, want)
	}
}

func TestRequests(t *testing.T) {
	usedLimit := usage.Meter{Unit: usage.UnitRequests, Used: usage.Ptr(11.6), Limit: usage.Ptr(300.0)}
	if got := requests(usedLimit); got != "11.6 / 300" {
		t.Errorf("used/limit => %q, want %q", got, "11.6 / 300")
	}
	remOnly := usage.Meter{Unit: usage.UnitRequests, Remaining: usage.Ptr(288.4)}
	if got := requests(remOnly); got != "288.4 left" {
		t.Errorf("remaining => %q, want %q", got, "288.4 left")
	}
	// Unlimited marker (no counts) and non-requests units produce nothing.
	if got := requests(usage.Meter{Unit: usage.UnitRequests}); got != "" {
		t.Errorf("empty requests meter => %q, want empty", got)
	}
	if got := requests(usage.Meter{Unit: usage.UnitUSD, Used: usage.Ptr(5.0), Limit: usage.Ptr(20.0)}); got != "" {
		t.Errorf("USD meter => %q, want empty (handled by money)", got)
	}
}

func TestRender_PartialFailureAndUnknownMarker(t *testing.T) {
	var buf bytes.Buffer
	results := []Result{
		{
			Name: "codex",
			Usage: &usage.Usage{
				Provider: "codex", Plan: "plus",
				Meters: []usage.Meter{
					{Key: "5h", Label: "5h limit", UsedPercent: usage.Ptr(26.0), Unit: usage.UnitPercent, Known: true},
					{Key: "spark", Label: "Spark", UsedPercent: usage.Ptr(50.0), Unit: usage.UnitPercent, Known: false},
				},
			},
		},
		{Name: "claude", Err: &usage.ReauthError{Provider: "claude"}},
	}
	Render(&buf, results, Options{})
	out := buf.String()
	if !strings.Contains(out, "5h limit") {
		t.Errorf("missing known meter line:\n%s", out)
	}
	if !strings.Contains(out, "Spark *") {
		t.Errorf("unknown meter should be marked with *:\n%s", out)
	}
	if !strings.Contains(out, "⚠") || !strings.Contains(out, "claude") {
		t.Errorf("error result should be shown:\n%s", out)
	}
}

func TestRender_EmojiSignals(t *testing.T) {
	var buf bytes.Buffer
	results := []Result{
		{
			Name: "codex",
			Usage: &usage.Usage{
				Provider: "codex", Plan: "plus",
				Meters: []usage.Meter{
					{Key: "ok", Label: "Low", UsedPercent: usage.Ptr(20.0), Unit: usage.UnitPercent, Known: true},
					{Key: "warn", Label: "Mid", UsedPercent: usage.Ptr(70.0), Unit: usage.UnitPercent, Known: true},
					{Key: "crit", Label: "High", UsedPercent: usage.Ptr(95.0), Unit: usage.UnitPercent, Known: true},
					{Key: "inf", Label: "Chat", Unit: usage.UnitPercent, Unlimited: true, Known: true},
				},
			},
		},
		{Name: "claude", Err: &usage.ReauthError{Provider: "claude"}},
		{Name: "cursor", Err: &usage.NotConfiguredError{Provider: "cursor"}},
	}
	Render(&buf, results, Options{Emoji: true})
	out := buf.String()
	for _, want := range []string{
		emojiOK + " Low",
		emojiWarn + " Mid",
		emojiCrit + " High",
		emojiNeutral + " Chat", // no-percent meter
		emojiError,             // genuine error
		emojiNeutral + " not configured",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("emoji output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("emoji mode must not emit ANSI escapes:\n%q", out)
	}
}

func TestRender_LossSignal(t *testing.T) {
	// 18% used with 75% of the window elapsed (proj ~24%) is tracking to waste
	// most of the quota: the line should read as a "loss" — blue in color mode,
	// 🔵 in emoji mode — and surface the projection.
	now := time.Now()
	start := now.Add(-75 * time.Hour)
	reset := now.Add(25 * time.Hour)
	meter := usage.Meter{
		Key: "weekly", Label: "Weekly limit", Unit: usage.UnitPercent, Known: true,
		UsedPercent: usage.Ptr(18.0), WindowStart: &start, ResetsAt: &reset,
	}
	res := []Result{{Name: "codex", Usage: &usage.Usage{Provider: "codex", Plan: "plus", Meters: []usage.Meter{meter}}}}

	var color bytes.Buffer
	Render(&color, res, Options{Color: true})
	if got := color.String(); !strings.Contains(got, "\x1b[34m") {
		t.Errorf("under-paced meter should be blue (\\x1b[34m):\n%q", got)
	}
	if got := color.String(); !strings.Contains(got, "proj 24%") {
		t.Errorf("projection should be shown to explain the color:\n%q", got)
	}
	if got := color.String(); !strings.Contains(got, "\x1b[1;97m") {
		t.Errorf("loss bar should use a bright-white pace marker:\n%q", got)
	}

	var emoji bytes.Buffer
	Render(&emoji, res, Options{Emoji: true})
	if got := emoji.String(); !strings.Contains(got, emojiLoss+" Weekly limit") {
		t.Errorf("under-paced meter should be prefixed with %s:\n%s", emojiLoss, got)
	}
}

func TestRender_OmitsAccount(t *testing.T) {
	var buf bytes.Buffer
	results := []Result{
		{
			Name: "codex",
			Usage: &usage.Usage{
				Provider: "codex", Plan: "Pro", Account: "alice@example.com",
				Meters: []usage.Meter{
					{Key: "5h", Label: "5h limit", UsedPercent: usage.Ptr(26.0), Unit: usage.UnitPercent, Known: true},
				},
			},
		},
	}
	Render(&buf, results, Options{})
	out := buf.String()
	if strings.Contains(out, "alice@example.com") {
		t.Errorf("account/email must not appear in human-readable output:\n%s", out)
	}
	// The plan still shows; only the account is suppressed.
	if !strings.Contains(out, "Pro") {
		t.Errorf("plan should still be shown in the header:\n%s", out)
	}
}

func TestRender_NotConfigured(t *testing.T) {
	var buf bytes.Buffer
	results := []Result{
		{Name: "cursor", Err: &usage.NotConfiguredError{Provider: "cursor"}},
		{Name: "codex", Err: &usage.NotConfiguredError{Provider: "codex", Reason: "ログインされていません"}},
	}
	Render(&buf, results, Options{})
	out := buf.String()
	if !strings.Contains(out, "not configured") {
		t.Errorf("not-configured provider should show a quiet note:\n%s", out)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("not-configured must not be shown as a warning:\n%s", out)
	}
	if !strings.Contains(out, "ログインされていません") {
		t.Errorf("reason should be surfaced when present:\n%s", out)
	}
}
