import { Group, Text, Tooltip } from "@mantine/core";

import type { Step } from "../api/types";
import { formatDuration, statusLabel } from "../lib/format";

const COLORS: Record<string, string> = {
  succeeded: "var(--mantine-color-green-6)",
  running: "var(--mantine-color-blue-5)",
  dispatched: "var(--mantine-color-blue-3)",
  scheduled: "var(--mantine-color-gray-5)",
  failed: "var(--mantine-color-red-6)",
  timed_out: "var(--mantine-color-red-4)",
  lost: "var(--mantine-color-grape-5)",
  cancelled: "var(--mantine-color-gray-6)",
};

/** Gantt of steps: one row per step (foreach items included), one bar per attempt. */
export function StepTimeline({ steps, t0, now }: { steps: Step[]; t0: number; now: number }) {
  const t1 = Math.max(now, t0 + 1000, ...steps.flatMap((s) => s.attempts.map((a) => (a.finished_at ? Date.parse(a.finished_at) : 0))));
  const span = t1 - t0;
  const pct = (t: number) => `${Math.max(0, Math.min(100, ((t - t0) / span) * 100))}%`;
  return (
    <div data-testid="step-timeline">
      {steps.map((s, i) => (
        <Group key={`${s.stage_index}:${s.step_id}:${s.item_index ?? ""}:${s.visits}:${i}`} gap="sm" wrap="nowrap" mb={4}>
          <Text size="xs" w={200} truncate ff="monospace" title={`${s.stage} / ${s.step_id}`}>
            {s.stage_index + 1}. {s.step_id}
            {s.item_index !== undefined ? `[${s.item_index}]` : ""}
            {s.visits > 1 ? ` ×${s.visits}` : ""}
          </Text>
          <div style={{ position: "relative", flex: 1, height: 18, background: "var(--mantine-color-default-hover)", borderRadius: 4 }}>
            {s.attempts.map((a) => {
              const start = a.started_at ? Date.parse(a.started_at) : null;
              if (start === null) return null;
              const end = a.finished_at ? Date.parse(a.finished_at) : now;
              return (
                <Tooltip key={a.attempt_id} label={`попытка ${a.number}: ${statusLabel(a.status)} · ${formatDuration(end - start)}${a.replica_id ? ` · ${a.replica_id}` : ""}${a.error ? ` · ${a.error.code}: ${a.error.message}` : ""}`} withinPortal>
                  <div className="mf-timeline-bar" style={{ left: pct(start), width: `calc(${pct(end)} - ${pct(start)})`, background: COLORS[a.status] ?? "gray" }} />
                </Tooltip>
              );
            })}
          </div>
          <Text size="xs" c="dimmed" w={70} ta="right">
            {formatDuration(s.started_at ? (s.finished_at ? Date.parse(s.finished_at) : now) - Date.parse(s.started_at) : undefined)}
          </Text>
        </Group>
      ))}
    </div>
  );
}
