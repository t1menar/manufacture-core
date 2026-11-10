import { LineChart } from "@mantine/charts";
import { Card, Text } from "@mantine/core";

import type { MetricPoint } from "../api/types";
import { formatTime } from "../lib/format";

export type ChartSeries = { name: string; label: string; color: string; points: MetricPoint[] };

/** Merges `[unix_s, value]` series on their timestamps for Recharts. */
export function mergeSeries(series: ChartSeries[]): Record<string, number | string>[] {
  const rows = new Map<number, Record<string, number | string>>();
  for (const s of series) {
    for (const [ts, v] of s.points) {
      const row = rows.get(ts) ?? { ts, t: formatTime(ts * 1000).slice(0, 5) };
      row[s.name] = v;
      rows.set(ts, row);
    }
  }
  return [...rows.values()].sort((a, b) => Number(a.ts) - Number(b.ts));
}

export function MetricChart({ title, unit, series, h = 180 }: { title: string; unit?: string; series: ChartSeries[]; h?: number }) {
  const data = mergeSeries(series);
  const last = series.map((s) => s.points.at(-1)?.[1]);
  return (
    <Card withBorder p="sm" radius="md" data-testid={`chart-${title}`}>
      <Text size="sm" fw={600}>
        {title}
        <Text span c="dimmed" size="xs" ml={6}>
          {last.map((v, i) => (v === undefined ? "" : `${series[i]!.label} ${Math.round(v * 100) / 100}${unit ?? ""}`)).filter(Boolean).join(" · ")}
        </Text>
      </Text>
      <LineChart
        h={h}
        data={data}
        dataKey="t"
        series={series.map((s) => ({ name: s.name, label: s.label, color: s.color }))}
        curveType="monotone"
        withDots={false}
        strokeWidth={1.6}
        gridAxis="y"
        tickLine="none"
        unit={unit}
        withLegend={series.length > 1}
        legendProps={{ verticalAlign: "top", height: 24 }}
        connectNulls
      />
    </Card>
  );
}
