import { Badge, Button, Card, Drawer, Group, Paper, ScrollArea, SegmentedControl, Select, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { lazy, Suspense, useEffect, useMemo, useState } from "react";

import { api } from "../api/client";
import { q } from "../api/queries";
import { StatusBadge } from "../components/StatusBadge";
import { formatDuration, formatRate, formatTime } from "../lib/format";
import { showError, showOk } from "../lib/toasts";
import { useLive } from "../live/store";
import { Conveyor2D } from "../scene/Conveyor2D";
import { buildScene, laneModel } from "../scene/model";
import { webglAvailable } from "../scene/webgl";

const Conveyor3D = lazy(() => import("../scene/Conveyor3D").then((m) => ({ default: m.Conveyor3D })));

function Stat({ label, value, color }: { label: string; value: string | number; color?: string }) {
  return (
    <Paper withBorder p="sm" radius="md">
      <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
        {label}
      </Text>
      <Text fz={22} fw={700} c={color}>
        {value}
      </Text>
    </Paper>
  );
}

function ModulePanel({ name, onClose }: { name: string; onClose: () => void }) {
  const st = useLive((s) => s.modules[name]);
  const m = useLive((s) => s.metrics[name]);
  const hist = useLive((s) => s.rateHistory[name]);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const scale = async (delta: number) => {
    if (!st) return;
    try {
      await api.scaleModule(name, Math.max(0, st.replicas.desired + delta));
      void qc.invalidateQueries({ queryKey: ["modules"] });
      showOk(`${name}: ${st.replicas.desired + delta} реплик`);
    } catch (e) {
      showError(e);
    }
  };
  const max = Math.max(0.01, ...(hist ?? [0]));
  return (
    <Stack>
      <Group justify="space-between">
        <StatusBadge status={st?.health} size="lg" />
        <Button
          variant="light"
          size="xs"
          onClick={() => {
            onClose();
            void navigate({ to: "/modules/$name", params: { name } });
          }}
        >
          Подробнее →
        </Button>
      </Group>
      <Text size="xs" c="dimmed" ff="monospace" style={{ wordBreak: "break-all" }}>
        {st?.image ?? "—"}
      </Text>
      <SimpleGrid cols={2} spacing="xs">
        <Stat label="реплики" value={st ? `${st.replicas.ready}/${st.replicas.desired}` : "—"} />
        <Stat label="throughput" value={formatRate(m?.rate)} />
        <Stat label="p50 / p95" value={m ? `${formatDuration(m.p50_ms)} / ${formatDuration(m.p95_ms)}` : "—"} />
        <Stat label="в работе" value={m?.in_flight ?? st?.in_flight ?? 0} />
        <Stat label="очередь" value={m?.queue_depth ?? st?.queue_depth ?? 0} color={(m?.queue_depth ?? 0) > 10 ? "orange" : undefined} />
        <Stat label="ошибки" value={`${Math.round((m?.error_rate ?? 0) * 100)}%`} color={(m?.error_rate ?? 0) > 0.1 ? "red" : undefined} />
      </SimpleGrid>
      <div>
        <Text size="xs" c="dimmed" mb={4}>
          throughput за минуту
        </Text>
        <svg viewBox="0 0 120 30" width="100%" height={48} preserveAspectRatio="none" aria-label="throughput sparkline">
          <polyline fill="none" stroke="#38bdf8" strokeWidth={1.2} points={(hist ?? []).map((v, i) => `${(i / 59) * 120},${30 - (v / max) * 28}`).join(" ")} />
        </svg>
      </div>
      <Group>
        <Button variant="default" size="xs" onClick={() => void scale(-1)} disabled={!st || st.replicas.desired === 0}>
          − реплика
        </Button>
        <Button variant="default" size="xs" onClick={() => void scale(1)} disabled={!st}>
          + реплика
        </Button>
      </Group>
      {st?.conditions.map((c) => (
        <Text key={c.type} size="xs" c={c.status === "True" ? "dimmed" : "orange"}>
          {c.type}: {c.status}
          {c.reason ? ` (${c.reason})` : ""}
          {c.message ? ` — ${c.message}` : ""}
        </Text>
      ))}
    </Stack>
  );
}

const HAS_WEBGL = typeof document !== "undefined" && webglAvailable();

export function OverviewPage() {
  const search: { pipeline?: string; view?: string } = useSearch({ strict: false });
  const navigate = useNavigate();
  const pipelinesQ = useQuery({ ...q.pipelines(), refetchInterval: 10_000 });
  const pipelines = pipelinesQ.data ?? [];
  const name = search.pipeline ?? pipelines[0]?.name;
  const current = pipelines.find((p) => p.name === name);
  const [view, setView] = useState<"3d" | "2d">(search.view === "2d" || !HAS_WEBGL ? "2d" : "3d");
  const [selected, setSelected] = useState<string | null>(null);
  const [stats, setStats] = useState({ fps: 0, items: 0, total: 0, effects: true });
  const modules = useLive((s) => s.modules);
  const metrics = useLive((s) => s.metrics);
  const allFeed = useLive((s) => s.feed);
  const feed = useMemo(() => allFeed.filter((f) => f.pipeline === undefined || f.pipeline === name), [allFeed, name]);
  const items = useLive((s) => s.items);
  const seedConveyor = useLive((s) => s.seedConveyor);
  const qc = useQueryClient();

  // Items for the selected pipeline come from its conveyor snapshot, then live events.
  const conveyorQ = useQuery({ ...q.conveyor(name ?? ""), enabled: Boolean(name), staleTime: Infinity });
  useEffect(() => {
    if (conveyorQ.data) seedConveyor(conveyorQ.data);
  }, [conveyorQ.data, seedConveyor]);

  const detailQ = useQuery({ ...q.pipeline(name ?? ""), enabled: Boolean(name) });
  const source = triggerSource(detailQ.data?.spec);
  const scene = useMemo(() => {
    if (!current) return null;
    const sc = buildScene({ name: current.name, revision: current.revision, stages: current.stages, modules, metrics, items: {} });
    if (source) sc.source = laneModel("source", source, modules, metrics);
    return sc;
  }, [current, modules, metrics, source]);

  const counts = useMemo(() => {
    let active = 0;
    let attention = 0;
    let failed = 0;
    for (const it of Object.values(items)) {
      if (it.pipeline !== name) continue;
      if (it.state === "attention") attention++;
      else if (it.state === "failed") failed++;
      else if (!it.finishedAt) active++;
    }
    return { active, attention, failed };
  }, [items, name]);
  const throughput = scene ? scene.stages.at(-1)?.lanes.reduce((a, l) => a + l.live.rate, 0) ?? 0 : 0;

  const run = async () => {
    if (!name) return;
    try {
      const r = await api.startRun(name);
      showOk(`Запуск ${r.id.slice(0, 8)} создан`);
      void qc.invalidateQueries({ queryKey: ["runs"] });
    } catch (e) {
      showError(e);
    }
  };

  const selectRun = (id: string) => void navigate({ to: "/runs/$id", params: { id } });

  return (
    <Stack gap="sm" h="calc(100vh - 56px - 2 * var(--mantine-spacing-md))">
      <Group justify="space-between" wrap="wrap">
        <Group gap="sm">
          <Title order={3}>Конвейер</Title>
          <Select
            aria-label="Пайплайн"
            data={pipelines.map((p) => ({ value: p.name, label: `${p.name} · r${p.revision}` }))}
            value={name ?? null}
            onChange={(v) => v && void navigate({ to: "/", search: { pipeline: v } })}
            w={240}
            allowDeselect={false}
            size="sm"
          />
          {current && <Badge variant="light">{current.stages.length} стадий · {new Set(current.stages.flatMap((s) => s.lanes.map((l) => l.module))).size} модулей</Badge>}
        </Group>
        <Group gap="sm">
          <SegmentedControl
            size="xs"
            value={view}
            onChange={(v) => setView(v)}
            data={[
              { value: "3d", label: "3D", disabled: !HAS_WEBGL },
              { value: "2d", label: "2D" },
            ]}
          />
          <Button size="xs" onClick={() => void run()} disabled={!name}>
            ▶ Запустить
          </Button>
        </Group>
      </Group>
      <SimpleGrid cols={{ base: 2, md: 5 }} spacing="sm">
        <Stat label="на ленте" value={counts.active} />
        <Stat label="в очереди" value={current?.runs.queued ?? 0} />
        <Stat label="нужно внимание" value={counts.attention} color={counts.attention ? "orange" : undefined} />
        <Stat label="выход / с" value={throughput.toFixed(2)} color="teal" />
        <Stat label="успешно / ошибок" value={`${current?.runs.succeeded_24h ?? "—"} / ${current?.runs.failed_24h ?? "—"}`} />
      </SimpleGrid>
      <Group align="stretch" gap="sm" wrap="nowrap" style={{ flex: 1, minHeight: 0 }}>
        <div className="mf-scene" style={{ flex: 1 }}>
          {scene &&
            (view === "3d" ? (
              <Suspense fallback={<Conveyor2D scene={scene} selectedModule={selected} onSelectModule={setSelected} onSelectRun={selectRun} />}>
                <Conveyor3D scene={scene} selectedModule={selected} onSelectModule={setSelected} onSelectRun={selectRun} onStats={setStats} />
              </Suspense>
            ) : (
              <Conveyor2D scene={scene} selectedModule={selected} onSelectModule={setSelected} onSelectRun={selectRun} />
            ))}
          {!scene && !pipelinesQ.isPending && (
            <Stack align="center" justify="center" h="100%">
              <Text c="dimmed">Пайплайнов пока нет.</Text>
              <Button component={Link} to="/pipelines/new" variant="light">
                Применить YAML
              </Button>
            </Stack>
          )}
          {view === "3d" && scene && (
            <div className="mf-scene-hud" style={{ left: 12, bottom: 10 }} data-testid="scene-stats">
              {stats.fps} fps · {stats.items} изделий{stats.total > stats.items ? ` (+${stats.total - stats.items})` : ""} · fx {stats.effects ? "on" : "off"}
            </div>
          )}
          <div className="mf-scene-hud" style={{ right: 12, bottom: 10 }}>
            <Group gap={10}>
              {(
                [
                  ["#38bdf8", "едет"],
                  ["#e0f2fe", "в работе"],
                  ["#4ade80", "готово"],
                  ["#fb923c", "внимание"],
                  ["#f43f5e", "брак"],
                ] as const
              ).map(([c, l]) => (
                <Group key={l} gap={4}>
                  <span className="mf-live-dot" style={{ background: c }} />
                  {l}
                </Group>
              ))}
            </Group>
          </div>
        </div>
        <Card withBorder w={300} p="sm" visibleFrom="lg" style={{ display: "flex", flexDirection: "column" }}>
          <Text fw={600} size="sm" mb="xs">
            События
          </Text>
          <ScrollArea style={{ flex: 1 }} type="auto">
            <Stack gap={6}>
              {feed.length === 0 && (
                <Text size="xs" c="dimmed">
                  Ждём событий…
                </Text>
              )}
              {feed.map((f) => (
                <Group key={f.key} gap={6} wrap="nowrap" align="flex-start">
                  <Text size="xs" c="dimmed" ff="monospace" style={{ flexShrink: 0 }}>
                    {formatTime(f.at)}
                  </Text>
                  <Text size="xs" c={f.tone === "error" ? "red.4" : f.tone === "warn" ? "orange.4" : f.tone === "ok" ? "green.4" : undefined} style={{ wordBreak: "break-word" }}>
                    {f.text}
                  </Text>
                </Group>
              ))}
            </Stack>
          </ScrollArea>
        </Card>
      </Group>
      <Drawer opened={Boolean(selected)} onClose={() => setSelected(null)} position="right" title={<Text fw={700}>{selected}</Text>} size="sm" withOverlay={false} lockScroll={false}>
        {selected && <ModulePanel name={selected} onClose={() => setSelected(null)} />}
      </Drawer>
    </Stack>
  );
}

/** `spec.trigger.source` of the applied Pipeline document: that module is drawn at the belt entry. */
function triggerSource(doc: Record<string, unknown> | undefined): string | undefined {
  const spec = doc?.spec as { trigger?: { source?: unknown } } | undefined;
  return typeof spec?.trigger?.source === "string" ? spec.trigger.source : undefined;
}
