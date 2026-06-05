// Package render prints usage snapshots as a compact, CodexBar-like summary.
package render

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kohii/aiquota/internal/usage"
)

// Result pairs a provider name with its fetched usage or the error that
// occurred, so partial failures can be displayed alongside successes.
type Result struct {
	Name  string
	Usage *usage.Usage
	Err   error
}

const (
	labelWidth = 18
	barWidth   = 16
)

// ANSI helpers (no-ops when color is disabled).
type palette struct{ on bool }

func (p palette) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func (p palette) bold(s string) string   { return p.wrap("1", s) }
func (p palette) dim(s string) string    { return p.wrap("2", s) }
func (p palette) cyan(s string) string   { return p.wrap("36", s) }
func (p palette) green(s string) string  { return p.wrap("32", s) }
func (p palette) yellow(s string) string { return p.wrap("33", s) }
func (p palette) red(s string) string    { return p.wrap("31", s) }
func (p palette) blue(s string) string   { return p.wrap("34", s) }

// Options controls how Render styles output.
type Options struct {
	// Color emits ANSI styling (used when stdout is a TTY).
	Color bool
	// Emoji prefixes each body line with a 🔵/🟢/🟡/🔴/⚪ signal so the usage level
	// reads at a glance where ANSI color isn't rendered — e.g. Raycast's
	// fullOutput, which shows monospaced plain text but ignores ANSI. In
	// practice it is used instead of Color, not alongside it.
	Emoji bool
}

const (
	emojiLoss    = "🔵"
	emojiOK      = "🟢"
	emojiWarn    = "🟡"
	emojiCrit    = "🔴"
	emojiNeutral = "⚪"
	emojiError   = "⚠️"
)

// Render writes all results to w using opt for styling.
func Render(w io.Writer, results []Result, opt Options) {
	p := palette{on: opt.Color}
	now := time.Now()
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, header(p, r))
		if r.Err != nil {
			// "Not configured" (tool absent / not logged in) is shown quietly,
			// not as a warning, and with any short reason the provider gave.
			var nc *usage.NotConfiguredError
			if errors.As(r.Err, &nc) {
				note := "not configured"
				if nc.Reason != "" {
					note += " · " + nc.Reason
				}
				if opt.Emoji {
					fmt.Fprintf(w, "%s %s\n", emojiNeutral, note)
				} else {
					fmt.Fprintf(w, "  %s\n", p.dim("– "+note))
				}
				continue
			}
			if opt.Emoji {
				fmt.Fprintf(w, "%s %s\n", emojiError, r.Err.Error())
			} else {
				fmt.Fprintf(w, "  %s %s\n", p.red("⚠"), r.Err.Error())
			}
			continue
		}
		if r.Usage == nil || len(r.Usage.Meters) == 0 {
			if opt.Emoji {
				fmt.Fprintf(w, "%s (no usage data)\n", emojiNeutral)
			} else {
				fmt.Fprintf(w, "  %s\n", p.dim("(no usage data)"))
			}
			continue
		}
		for _, m := range r.Usage.Meters {
			fmt.Fprintln(w, meterLine(p, opt, m, now))
		}
	}
}

// header renders the provider title line as "provider · plan". The account
// (an email for codex/cursor, a login for copilot) is deliberately omitted from
// the human-readable output to avoid leaking it in shared terminals/screenshots;
// it is still emitted in --json (which serializes usage.Usage directly).
func header(p palette, r Result) string {
	parts := []string{p.bold(p.cyan(r.Name))}
	if r.Usage != nil && r.Usage.Plan != "" {
		parts = append(parts, r.Usage.Plan)
	}
	return strings.Join(parts, " · ")
}

func meterLine(p palette, opt Options, m usage.Meter, now time.Time) string {
	label := m.Label
	if label == "" {
		label = m.Key
	}
	if m.Unlimited {
		label = label + " (unlimited)"
	}
	if !m.Known {
		label = label + " *"
	}
	label = padRight(label, labelWidth)

	var bar, value, proj string
	var pace *float64
	var lvl *level
	if m.UsedPercent != nil {
		pct := *m.UsedPercent
		pace = meterPace(m, now)
		l := levelOf(pct, pace)
		lvl = &l
		bar = renderBar(p, l, pct, pace)
		value = p.colorLevel(l, fmt.Sprintf("%5.1f%%", pct))
		// Surface the projection that drives the color once the window is far
		// enough along to trust it, so a cold/blue (or hot/red) line is legible.
		if usablePace(pace) && *pace >= overrunFloor {
			proj = p.dim(fmt.Sprintf("proj %.0f%%", pct / *pace * 100))
		}
	} else {
		bar = strings.Repeat(" ", barWidth+2) // keep columns aligned
	}

	extras := []string{}
	if amt := money(m); amt != "" {
		extras = append(extras, amt)
	}
	if cnt := requests(m); cnt != "" {
		extras = append(extras, cnt)
	}
	if m.Unit == usage.UnitCredits && m.Used != nil {
		extras = append(extras, fmt.Sprintf("%g credits", *m.Used))
	}
	if pace != nil {
		extras = append(extras, p.dim(fmt.Sprintf("pace %.0f%%", *pace)))
	}
	if proj != "" {
		extras = append(extras, proj)
	}
	if m.ResetsAt != nil {
		extras = append(extras, p.dim("resets "+humanReset(*m.ResetsAt)))
	}

	line := linePrefix(opt, lvl) + label + " " + bar
	if value != "" {
		line += "  " + value
	}
	if len(extras) > 0 {
		line += "  " + p.dim("·") + " " + strings.Join(extras, "  ")
	}
	return line
}

// linePrefix is the per-line lead-in: a signal emoji in Emoji mode (carrying the
// level that ANSI color otherwise would), else the plain two-space indent. lvl is
// nil for meters with no percent to classify (e.g. unlimited quotas).
func linePrefix(opt Options, lvl *level) string {
	if !opt.Emoji {
		return "  "
	}
	if lvl == nil {
		return emojiNeutral + " "
	}
	return emojiFor(*lvl) + " "
}

// money formats USD used/limit when present.
func money(m usage.Meter) string {
	if m.Unit != usage.UnitUSD {
		return ""
	}
	switch {
	case m.Used != nil && m.Limit != nil:
		return fmt.Sprintf("$%.2f / $%.2f", *m.Used, *m.Limit)
	case m.Used != nil:
		return fmt.Sprintf("$%.2f", *m.Used)
	default:
		return ""
	}
}

// requests formats used/limit (or remaining) counts for request-style meters,
// e.g. GitHub Copilot's premium interactions. Counts can be fractional, so %g
// is used. Returns "" when there is nothing meaningful to show.
func requests(m usage.Meter) string {
	if m.Unit != usage.UnitRequests {
		return ""
	}
	switch {
	case m.Used != nil && m.Limit != nil:
		return fmt.Sprintf("%g / %g", round1(*m.Used), round1(*m.Limit))
	case m.Remaining != nil:
		return fmt.Sprintf("%g left", round1(*m.Remaining))
	default:
		return ""
	}
}

// round1 rounds to one decimal place to keep fractional request counts tidy.
func round1(f float64) float64 { return math.Round(f*10) / 10 }

// renderBar draws the usage bar. When pace is non-nil, the cell at the elapsed
// position is replaced with a marker (│) so usage can be compared against how
// far through the window "now" is: filled left of the marker means usage is
// behind the clock (headroom), filled past it means burning faster than time.
func renderBar(p palette, lvl level, pct float64, pace *float64) string {
	filled := clampCells(pct)
	cells := make([]rune, barWidth)
	for i := range cells {
		if i < filled {
			cells[i] = '█'
		} else {
			cells[i] = '░'
		}
	}

	if pace == nil {
		return "[" + p.colorLevel(lvl, string(cells)) + "]"
	}

	pos := clampCells(*pace)
	if pos >= barWidth {
		pos = barWidth - 1
	}
	// Bold cyan reads well over green/yellow/red bars, but blends into a blue
	// (loss) bar — switch to bold bright-white there so the marker stays visible.
	markerCode := "1;36"
	if lvl == levelLoss {
		markerCode = "1;97"
	}
	left := p.colorLevel(lvl, string(cells[:pos]))
	marker := p.wrap(markerCode, "│")
	right := p.colorLevel(lvl, string(cells[pos+1:]))
	return "[" + left + marker + right + "]"
}

// clampCells maps a 0-100 percentage to a bar cell count in [0, barWidth].
func clampCells(pct float64) int {
	n := int(math.Round(pct / 100 * barWidth))
	if n < 0 {
		return 0
	}
	if n > barWidth {
		return barWidth
	}
	return n
}

// meterPace returns the elapsed fraction (0-100) of the meter's reset window, or
// nil when the window bounds are unknown.
func meterPace(m usage.Meter, now time.Time) *float64 {
	if m.WindowStart != nil && m.ResetsAt != nil {
		return ptr(pacePercent(*m.WindowStart, *m.ResetsAt, now))
	}
	return nil
}

// pacePercent returns how far through the [start, reset] window now is, 0-100.
func pacePercent(start, reset, now time.Time) float64 {
	total := reset.Sub(start)
	if total <= 0 {
		return 0
	}
	pct := now.Sub(start).Seconds() / total.Seconds() * 100
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

func ptr(f float64) *float64 { return &f }

// level classifies how a metered quota is tracking against its reset window.
// The ordering Loss < Good < Warn < Crit lets the absolute "near cap" net escalate
// with a plain comparison; Loss is assigned only in the pace branch and never
// participates in those comparisons.
type level int

const (
	levelLoss level = iota // tracking to finish well under cap — paying for unused quota
	levelGood              // on track, or no reason for concern
	levelWarn              // tracking to exhaust the quota somewhat early
	levelCrit              // nearly out now, or tracking to run out well before reset
)

const (
	absWarn = 60.0 // absolute-usage warn net, used when pace cannot decide
	absCrit = 85.0 // absolute-usage crit net — nearly out *now*, regardless of pace

	overrunFloor = 20.0 // min elapsed % before a projection may warn/crit (over-burn)
	lossFloor    = 25.0 // min elapsed % before a projection may flag loss (under-burn)

	projLoss = 60.0  // projected end-of-window % below this → loss (use-it-or-lose-it)
	projWarn = 110.0 // projected % at/above → warn
	projCrit = 140.0 // projected % at/above → crit
)

// levelOf classifies a meter by where it is tracking to finish the reset window,
// projecting current usage to reset (projected = pct / pace). The framing is
// use-it-or-lose-it: a quota tracking to finish far under cap is "loss" (you are
// paying for headroom you won't use), shown cold/blue to nudge "use more"; one
// tracking to exhaust early is warn/crit. Absolute nets still flag a quota already
// nearly spent, and cover windows with no clock or one too early to project.
func levelOf(pct float64, pace *float64) level {
	if pct >= absCrit {
		return levelCrit
	}
	if usablePace(pace) && *pace >= overrunFloor {
		projected := pct / *pace * 100
		switch {
		case projected >= projCrit:
			return levelCrit
		case projected >= projWarn:
			return levelWarn
		case *pace >= lossFloor && projected < projLoss:
			return levelLoss
		default:
			return levelGood
		}
	}
	if pct >= absWarn {
		return levelWarn
	}
	return levelGood
}

// usablePace reports whether pace is a finite, positive elapsed fraction we can
// divide by to project end-of-window usage.
func usablePace(pace *float64) bool {
	return pace != nil && *pace > 0 && !math.IsNaN(*pace) && !math.IsInf(*pace, 0)
}

func (p palette) colorLevel(l level, s string) string {
	switch l {
	case levelLoss:
		return p.blue(s)
	case levelWarn:
		return p.yellow(s)
	case levelCrit:
		return p.red(s)
	default:
		return p.green(s)
	}
}

func emojiFor(l level) string {
	switch l {
	case levelLoss:
		return emojiLoss
	case levelWarn:
		return emojiWarn
	case levelCrit:
		return emojiCrit
	default:
		return emojiOK
	}
}

// humanReset renders a reset time: compact relative within 48h, else a date.
func humanReset(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return "now"
	}
	if d < 48*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		switch {
		case h >= 24:
			return fmt.Sprintf("%dd%dh", h/24, h%24)
		case h > 0:
			return fmt.Sprintf("%dh%dm", h, m)
		default:
			return fmt.Sprintf("%dm", m)
		}
	}
	return t.Local().Format("Jan 2 15:04")
}

func padRight(s string, n int) string {
	w := utf8.RuneCountInString(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}
