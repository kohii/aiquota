// Package render prints usage snapshots as a compact, CodexBar-like summary.
package render

import (
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

// Render writes all results to w. color enables ANSI styling.
func Render(w io.Writer, results []Result, color bool) {
	p := palette{on: color}
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, header(p, r))
		if r.Err != nil {
			fmt.Fprintf(w, "  %s %s\n", p.red("⚠"), r.Err.Error())
			continue
		}
		if r.Usage == nil || len(r.Usage.Meters) == 0 {
			fmt.Fprintf(w, "  %s\n", p.dim("(no usage data)"))
			continue
		}
		for _, m := range r.Usage.Meters {
			fmt.Fprintln(w, "  "+meterLine(p, m))
		}
	}
}

func header(p palette, r Result) string {
	parts := []string{p.bold(p.cyan(r.Name))}
	if r.Usage != nil {
		if r.Usage.Plan != "" {
			parts = append(parts, r.Usage.Plan)
		}
		if r.Usage.Account != "" {
			parts = append(parts, p.dim(r.Usage.Account))
		}
	}
	return strings.Join(parts, " · ")
}

func meterLine(p palette, m usage.Meter) string {
	label := m.Label
	if label == "" {
		label = m.Key
	}
	if !m.Known {
		label = label + " *"
	}
	label = padRight(label, labelWidth)

	var bar, value string
	var pace *float64
	if m.UsedPercent != nil {
		pct := *m.UsedPercent
		if m.WindowStart != nil && m.ResetsAt != nil {
			pace = ptr(pacePercent(*m.WindowStart, *m.ResetsAt, time.Now()))
		}
		bar = renderBar(p, pct, pace)
		value = colorByPct(p, pct, fmt.Sprintf("%5.1f%%", pct))
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
	if m.ResetsAt != nil {
		extras = append(extras, p.dim("resets "+humanReset(*m.ResetsAt)))
	}

	line := label + " " + bar
	if value != "" {
		line += "  " + value
	}
	if len(extras) > 0 {
		line += "  " + p.dim("·") + " " + strings.Join(extras, "  ")
	}
	return line
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
func renderBar(p palette, pct float64, pace *float64) string {
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
		return "[" + colorByPct(p, pct, string(cells)) + "]"
	}

	pos := clampCells(*pace)
	if pos >= barWidth {
		pos = barWidth - 1
	}
	left := colorByPct(p, pct, string(cells[:pos]))
	marker := p.wrap("1;36", "│") // bold cyan, visible over filled or empty cells
	right := colorByPct(p, pct, string(cells[pos+1:]))
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

func colorByPct(p palette, pct float64, s string) string {
	switch {
	case pct >= 85:
		return p.red(s)
	case pct >= 60:
		return p.yellow(s)
	default:
		return p.green(s)
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
