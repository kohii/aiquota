import { describe, it, expect } from "vitest";
import {
  level,
  clamp01,
  pacePercent,
  humanReset,
  valueText,
  binaryCandidates,
  round1,
  titleCase,
  Meter,
} from "./lib";

describe("level", () => {
  it("matches CLI thresholds", () => {
    expect(level(0)).toBe("ok");
    expect(level(59.9)).toBe("ok");
    expect(level(60)).toBe("warn");
    expect(level(84.9)).toBe("warn");
    expect(level(85)).toBe("crit");
    expect(level(100)).toBe("crit");
  });
});

describe("clamp01", () => {
  it("clamps to 0..1", () => {
    expect(clamp01(-5)).toBe(0);
    expect(clamp01(0)).toBe(0);
    expect(clamp01(50)).toBe(0.5);
    expect(clamp01(100)).toBe(1);
    expect(clamp01(150)).toBe(1);
    expect(clamp01(NaN)).toBe(0);
  });
});

describe("pacePercent", () => {
  const start = "2026-05-01T00:00:00Z";
  const end = "2026-05-11T00:00:00Z"; // 10-day window

  it("is linear across the window", () => {
    expect(
      pacePercent(start, end, new Date("2026-05-06T00:00:00Z")),
    ).toBeCloseTo(50, 5);
    expect(
      pacePercent(start, end, new Date("2026-05-02T12:00:00Z")),
    ).toBeCloseTo(15, 5);
  });

  it("clamps outside the window", () => {
    expect(pacePercent(start, end, new Date("2026-04-01T00:00:00Z"))).toBe(0);
    expect(pacePercent(start, end, new Date("2026-06-01T00:00:00Z"))).toBe(100);
  });

  it("returns null for missing or degenerate bounds", () => {
    expect(pacePercent(undefined, end, new Date())).toBeNull();
    expect(pacePercent(start, undefined, new Date())).toBeNull();
    expect(pacePercent(end, start, new Date())).toBeNull(); // total <= 0
    expect(pacePercent("nonsense", end, new Date())).toBeNull();
  });
});

describe("humanReset", () => {
  const now = new Date("2026-05-31T12:00:00Z");

  it("returns now for past times", () => {
    expect(humanReset("2026-05-31T11:00:00Z", now)).toBe("now");
  });

  it("uses compact relative form within 48h", () => {
    expect(humanReset("2026-05-31T12:45:00Z", now)).toBe("45m");
    expect(humanReset("2026-05-31T15:08:00Z", now)).toBe("3h8m");
    expect(humanReset("2026-06-01T16:00:00Z", now)).toBe("1d4h");
  });

  it("returns empty for missing/invalid", () => {
    expect(humanReset(undefined, now)).toBe("");
    expect(humanReset("nope", now)).toBe("");
  });
});

describe("valueText", () => {
  it("formats usd", () => {
    expect(valueText({ unit: "usd", used: 8.3, limit: 40.23 } as Meter)).toBe(
      "$8.30 / $40.23",
    );
    expect(valueText({ unit: "usd", used: 8.3 } as Meter)).toBe("$8.30");
  });

  it("formats request counts and hides unlimited", () => {
    expect(
      valueText({ unit: "requests", used: 11.6, limit: 300 } as Meter),
    ).toBe("11.6 / 300");
    expect(valueText({ unit: "requests", remaining: 288.4 } as Meter)).toBe(
      "288.4 left",
    );
    expect(valueText({ unit: "requests", unlimited: true } as Meter)).toBe("");
  });

  it("formats credits and percent (none)", () => {
    expect(valueText({ unit: "credits", used: 12.34 } as Meter)).toBe(
      "12.3 credits",
    );
    expect(valueText({ unit: "percent", usedPercent: 50 } as Meter)).toBe("");
  });
});

describe("binaryCandidates", () => {
  it("honors a preference path exclusively", () => {
    expect(binaryCandidates("/custom/aiquota", "/Users/me")).toEqual([
      "/custom/aiquota",
    ]);
    expect(binaryCandidates("  /custom/aiquota  ", "/Users/me")).toEqual([
      "/custom/aiquota",
    ]);
  });

  it("falls back to common install locations in order", () => {
    expect(binaryCandidates("", "/Users/me")).toEqual([
      "/Users/me/go/bin/aiquota",
      "/opt/homebrew/bin/aiquota",
      "/usr/local/bin/aiquota",
      "/Users/me/.local/bin/aiquota",
    ]);
    expect(binaryCandidates(undefined, "/Users/me")[0]).toBe(
      "/Users/me/go/bin/aiquota",
    );
  });
});

describe("misc", () => {
  it("round1", () => {
    expect(round1(11.66)).toBe(11.7);
    expect(round1(300)).toBe(300);
  });
  it("titleCase", () => {
    expect(titleCase("codex")).toBe("Codex");
    expect(titleCase("")).toBe("");
  });
});
