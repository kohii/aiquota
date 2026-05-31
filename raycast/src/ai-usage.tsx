import {
  Action,
  ActionPanel,
  Color,
  Icon,
  List,
  getPreferenceValues,
  openExtensionPreferences,
} from "@raycast/api";
import { usePromise, getProgressIcon } from "@raycast/utils";
import { execFile } from "node:child_process";
import { accessSync, constants } from "node:fs";
import { homedir } from "node:os";
import { promisify } from "node:util";
import { useState, type ReactNode } from "react";
import {
  Result,
  Usage,
  Meter,
  Level,
  level,
  clamp01,
  pacePercent,
  humanReset,
  valueText,
  binaryCandidates,
  titleCase,
} from "./lib";

const execFileAsync = promisify(execFile);

interface Preferences {
  aiquotaPath?: string;
}

// PATH given to the child so a bare "aiquota" fallback can still resolve when
// none of the known absolute locations exist. Raycast's own PATH is minimal.
const childPath = [
  `${homedir()}/go/bin`,
  "/opt/homebrew/bin",
  "/usr/local/bin",
  `${homedir()}/.local/bin`,
  process.env.PATH ?? "",
].join(":");

/** resolveBinary returns the first executable aiquota path, or "aiquota" to let
 *  PATH resolve it. Throws a friendly error only when even that is missing. */
function resolveBinary(prefPath: string | undefined): string {
  for (const candidate of binaryCandidates(prefPath, homedir())) {
    try {
      accessSync(candidate, constants.X_OK);
      return candidate;
    } catch {
      // try next
    }
  }
  return "aiquota";
}

async function fetchUsage(): Promise<Result[]> {
  const { aiquotaPath } = getPreferenceValues<Preferences>();
  const bin = resolveBinary(aiquotaPath);
  try {
    const { stdout } = await execFileAsync(bin, ["--json"], {
      env: { ...process.env, PATH: childPath },
      timeout: 30_000,
      maxBuffer: 4 * 1024 * 1024,
    });
    return JSON.parse(stdout) as Result[];
  } catch (err) {
    const e = err as NodeJS.ErrnoException & { stdout?: string };
    // aiquota exits non-zero when every provider failed and none succeeded, but
    // it still prints the full JSON array (with per-provider error/notConfigured
    // entries). Prefer that stdout so failures render as red rows rather than a
    // whole-command error screen.
    const out = (e.stdout ?? "").trim();
    if (out.startsWith("[")) {
      try {
        return JSON.parse(out) as Result[];
      } catch {
        // not valid JSON after all — fall through
      }
    }
    if (e.code === "ENOENT") {
      throw new Error(
        "aiquota binary not found. Install with `go install github.com/kohii/aiquota/cmd/aiquota@latest`, or set its path in extension preferences.",
      );
    }
    throw err;
  }
}

const COLOR_BY_LEVEL: Record<Level, Color> = {
  ok: Color.Green,
  warn: Color.Yellow,
  crit: Color.Red,
};

export default function Command() {
  const [showDetail, setShowDetail] = useState(false);
  const { data, isLoading, error, revalidate } = usePromise(fetchUsage);

  if (error) {
    return (
      <List>
        <List.EmptyView
          icon={{ source: Icon.Warning, tintColor: Color.Red }}
          title="Could not read usage"
          description={error.message}
          actions={
            <ActionPanel>
              <Action
                title="Retry"
                icon={Icon.ArrowClockwise}
                onAction={revalidate}
              />
              <Action
                title="Open Extension Preferences"
                icon={Icon.Gear}
                onAction={openExtensionPreferences}
              />
              <Action.CopyToClipboard
                title="Copy Install Command"
                content="go install github.com/kohii/aiquota/cmd/aiquota@latest"
              />
            </ActionPanel>
          }
        />
      </List>
    );
  }

  const sharedActions = (
    <ActionPanel>
      <Action
        title="Refresh"
        icon={Icon.ArrowClockwise}
        onAction={revalidate}
        shortcut={{ modifiers: ["cmd"], key: "r" }}
      />
      <Action
        title={showDetail ? "Hide Details" : "Show Details"}
        icon={Icon.Sidebar}
        onAction={() => setShowDetail((v) => !v)}
        shortcut={{ modifiers: ["cmd"], key: "d" }}
      />
      <Action
        title="Open Extension Preferences"
        icon={Icon.Gear}
        onAction={openExtensionPreferences}
      />
    </ActionPanel>
  );

  return (
    <List isLoading={isLoading} isShowingDetail={showDetail}>
      {(data ?? []).map((r) => (
        <List.Section
          key={r.provider}
          title={titleCase(r.provider)}
          subtitle={sectionSubtitle(r)}
        >
          {sectionItems(r, showDetail, sharedActions)}
        </List.Section>
      ))}
    </List>
  );
}

function sectionSubtitle(r: Result): string | undefined {
  if (!r.usage) return undefined;
  const parts = [r.usage.plan, r.usage.account].filter(Boolean) as string[];
  return parts.length ? parts.join(" · ") : undefined;
}

function sectionItems(r: Result, showDetail: boolean, actions: ReactNode) {
  if (r.notConfigured) {
    return (
      <List.Item
        key={`${r.provider}:nc`}
        icon={{ source: Icon.Circle, tintColor: Color.SecondaryText }}
        title="Not configured"
        subtitle="tool not installed, or not logged in"
        actions={actions}
      />
    );
  }
  if (r.error) {
    return (
      <List.Item
        key={`${r.provider}:err`}
        icon={{ source: Icon.Warning, tintColor: Color.Red }}
        title="Error"
        subtitle={r.error}
        actions={actions}
      />
    );
  }
  const usage = r.usage;
  if (!usage || usage.meters.length === 0) {
    return (
      <List.Item
        key={`${r.provider}:empty`}
        icon={{ source: Icon.Dot, tintColor: Color.SecondaryText }}
        title="No usage data"
        actions={actions}
      />
    );
  }
  return usage.meters.map((m) => (
    <MeterItem
      key={`${r.provider}:${m.key}`}
      usage={usage}
      meter={m}
      showDetail={showDetail}
      actions={actions}
    />
  ));
}

function MeterItem({
  usage,
  meter,
  showDetail,
  actions,
}: {
  usage: Usage;
  meter: Meter;
  showDetail: boolean;
  actions: ReactNode;
}) {
  const now = new Date();
  const pct = meter.usedPercent;
  const pace = pacePercent(meter.windowStart, meter.resetsAt, now);
  const reset = humanReset(meter.resetsAt, now);
  const secondary = valueText(meter);

  const accessories: List.Item.Accessory[] = [];
  if (!showDetail) {
    if (pct != null) {
      accessories.push({
        tag: {
          value: `${Math.round(pct)}%`,
          color: COLOR_BY_LEVEL[level(pct)],
        },
      });
    } else if (meter.unlimited) {
      accessories.push({ text: "unlimited" });
    }
    if (secondary) accessories.push({ text: secondary });
    if (reset)
      accessories.push({
        icon: Icon.Clock,
        text: reset,
        tooltip: tooltipDate(meter.resetsAt),
      });
  }

  return (
    <List.Item
      icon={meterIcon(meter, pct)}
      title={meter.label + (meter.known ? "" : " *")}
      accessories={accessories}
      detail={
        showDetail ? (
          <MeterDetail usage={usage} meter={meter} pace={pace} reset={reset} />
        ) : undefined
      }
      actions={actions}
    />
  );
}

function meterIcon(meter: Meter, pct: number | undefined) {
  if (meter.unlimited)
    return { source: Icon.FullSignal, tintColor: Color.Green };
  if (pct != null)
    return getProgressIcon(clamp01(pct), COLOR_BY_LEVEL[level(pct)]);
  return { source: Icon.Dot, tintColor: Color.SecondaryText };
}

function MeterDetail({
  usage,
  meter,
  pace,
  reset,
}: {
  usage: Usage;
  meter: Meter;
  pace: number | null;
  reset: string;
}) {
  const L = List.Item.Detail.Metadata.Label;
  const num = (n: number | undefined) =>
    n != null ? String(Math.round(n * 100) / 100) : undefined;
  return (
    <List.Item.Detail
      metadata={
        <List.Item.Detail.Metadata>
          <L title="Provider" text={usage.provider} />
          {usage.plan ? <L title="Plan" text={usage.plan} /> : null}
          {usage.account ? <L title="Account" text={usage.account} /> : null}
          <List.Item.Detail.Metadata.Separator />
          <L title="Meter" text={meter.label} />
          {meter.unlimited ? <L title="Limit" text="unlimited" /> : null}
          {meter.usedPercent != null ? (
            <L title="Used" text={`${Math.round(meter.usedPercent)}%`} />
          ) : null}
          {pace != null ? (
            <L title="Window elapsed" text={`${Math.round(pace)}%`} />
          ) : null}
          {num(meter.used) ? (
            <L title="Used (count)" text={num(meter.used)!} />
          ) : null}
          {num(meter.limit) ? (
            <L title="Limit (count)" text={num(meter.limit)!} />
          ) : null}
          {num(meter.remaining) ? (
            <L title="Remaining" text={num(meter.remaining)!} />
          ) : null}
          {reset ? (
            <L
              title="Resets"
              text={`${reset}${tooltipDate(meter.resetsAt) ? ` (${tooltipDate(meter.resetsAt)})` : ""}`}
            />
          ) : null}
          <List.Item.Detail.Metadata.Separator />
          {usage.source ? <L title="Source" text={usage.source} /> : null}
          {usage.fetchedAt ? (
            <L
              title="Fetched"
              text={new Date(usage.fetchedAt).toLocaleString()}
            />
          ) : null}
        </List.Item.Detail.Metadata>
      }
    />
  );
}

function tooltipDate(iso: string | undefined): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  return Number.isNaN(t) ? "" : new Date(t).toLocaleString();
}
