// Pure presentation logic for the AI Usage view. Kept free of any @raycast/api
// import so it can be unit-tested in isolation (the .tsx maps these results to
// Raycast colors/icons). These mirror the CLI renderer in internal/render so
// the extension and `aiquota` show the same numbers.

/** Unit a meter's numeric values are measured in (matches the Go model). */
export type Unit = "percent" | "usd" | "credits" | "requests";

export interface Meter {
  key: string;
  label: string;
  usedPercent?: number;
  used?: number;
  limit?: number;
  remaining?: number;
  unit?: Unit;
  currency?: string;
  resetsAt?: string;
  windowStart?: string;
  unlimited?: boolean;
  known: boolean;
}

export interface Usage {
  provider: string;
  account?: string;
  plan?: string;
  meters: Meter[];
  source?: string;
  fetchedAt: string;
}

export interface Result {
  provider: string;
  usage?: Usage;
  notConfigured?: boolean;
  error?: string;
}

/** Severity of a usage percentage; mapped to a color in the view. */
export type Level = "ok" | "warn" | "crit";

/** level mirrors colorByPct in internal/render: >=85 crit, >=60 warn, else ok. */
export function level(pct: number): Level {
  if (pct >= 85) return "crit";
  if (pct >= 60) return "warn";
  return "ok";
}

/** clamp01 maps a 0-100 percentage to a 0..1 progress fraction. */
export function clamp01(pct: number): number {
  if (!Number.isFinite(pct)) return 0;
  if (pct <= 0) return 0;
  if (pct >= 100) return 1;
  return pct / 100;
}

/**
 * pacePercent returns how far through the [windowStart, resetsAt] window `now`
 * is, 0-100, or null when the bounds are missing/degenerate. Mirrors
 * pacePercent in internal/render. Computed client-side (not read from JSON) so
 * it stays correct as time passes between fetch and display.
 */
export function pacePercent(
  windowStart: string | undefined,
  resetsAt: string | undefined,
  now: Date,
): number | null {
  if (!windowStart || !resetsAt) return null;
  const start = Date.parse(windowStart);
  const end = Date.parse(resetsAt);
  if (Number.isNaN(start) || Number.isNaN(end)) return null;
  const total = end - start;
  if (total <= 0) return null;
  const pct = ((now.getTime() - start) / total) * 100;
  if (pct < 0) return 0;
  if (pct > 100) return 100;
  return pct;
}

const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];

/**
 * humanReset renders a reset time: "now" if past, a compact relative form
 * within 48h (e.g. "3h49m", "1d4h", "12m"), else a local date "Jan 2 15:04".
 * Mirrors humanReset in internal/render.
 */
export function humanReset(resetsAt: string | undefined, now: Date): string {
  if (!resetsAt) return "";
  const end = Date.parse(resetsAt);
  if (Number.isNaN(end)) return "";
  const ms = end - now.getTime();
  if (ms <= 0) return "now";
  if (ms < 48 * 3600 * 1000) {
    const totalMin = Math.floor(ms / 60000);
    const h = Math.floor(totalMin / 60);
    const m = totalMin % 60;
    if (h >= 24) return `${Math.floor(h / 24)}d${h % 24}h`;
    if (h > 0) return `${h}h${m}m`;
    return `${m}m`;
  }
  const d = new Date(end);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${MONTHS[d.getMonth()]} ${d.getDate()} ${hh}:${mm}`;
}

/** round1 rounds to one decimal place to keep fractional counts tidy. */
export function round1(n: number): number {
  return Math.round(n * 10) / 10;
}

/**
 * valueText formats a meter's secondary value (money / request counts / credits)
 * for an accessory, or "" when there is nothing meaningful to show. Mirrors
 * money()/requests() in internal/render.
 */
export function valueText(m: Meter): string {
  switch (m.unit) {
    case "usd":
      if (m.used != null && m.limit != null)
        return `$${m.used.toFixed(2)} / $${m.limit.toFixed(2)}`;
      if (m.used != null) return `$${m.used.toFixed(2)}`;
      return "";
    case "requests":
      if (m.unlimited) return "";
      if (m.used != null && m.limit != null)
        return `${round1(m.used)} / ${round1(m.limit)}`;
      if (m.remaining != null) return `${round1(m.remaining)} left`;
      return "";
    case "credits":
      if (m.used != null) return `${round1(m.used)} credits`;
      return "";
    default:
      return "";
  }
}

/**
 * binaryCandidates returns the ordered absolute paths to probe for the aiquota
 * binary, before falling back to a bare PATH lookup. A non-empty preference
 * path always wins. Pure (no fs access) so it is unit-testable; the caller
 * checks each path for an executable.
 */
export function binaryCandidates(
  prefPath: string | undefined,
  home: string,
): string[] {
  const trimmed = (prefPath ?? "").trim();
  if (trimmed) return [trimmed];
  return [
    `${home}/go/bin/aiquota`,
    "/opt/homebrew/bin/aiquota",
    "/usr/local/bin/aiquota",
    `${home}/.local/bin/aiquota`,
  ];
}

/** Capitalize a provider name for section titles ("codex" -> "Codex"). */
export function titleCase(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}
