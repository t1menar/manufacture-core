import { useEffect, useMemo, useState } from "react";

import { useLive } from "../live/store";
import { computeLayout, itemPosition, type Vec3 } from "./layout";
import { HEALTH_COLORS, STATE_COLORS, type ConveyorScene } from "./model";

const SCALE = 14;
const PAD = 40;

/**
 * Flat SVG view of the same layout: the fallback when WebGL is unavailable, a low-power mode,
 * and the compact preview in the pipeline editor. Items are re-projected at ~15 fps.
 */
export function Conveyor2D({
  scene,
  selectedModule,
  onSelectModule,
  onSelectRun,
  animate = true,
  height,
}: {
  scene: ConveyorScene;
  selectedModule?: string | null;
  onSelectModule?: (m: string | null) => void;
  onSelectRun?: (id: string) => void;
  animate?: boolean;
  height?: number | string;
}) {
  const layout = useMemo(() => computeLayout(scene), [scene]);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!animate) return;
    const t = setInterval(() => setNow(Date.now()), 66);
    return () => clearInterval(t);
  }, [animate]);
  const items = useLive((s) => s.items);

  const { min, max } = layout.bounds;
  const w = (max[0] - min[0]) * SCALE + PAD * 2;
  const h = (max[2] - min[2] + 4) * SCALE + PAD * 2;
  const X = (v: Vec3) => (v[0] - min[0]) * SCALE + PAD;
  const Y = (v: Vec3) => (v[2] - min[2] + 3) * SCALE + PAD;
  const pts = layout.path.map((p) => `${X(p)},${Y(p)}`).join(" ");

  const dots: { key: string; id: string; x: number; y: number; color: string }[] = [];
  if (animate) {
    const p: Vec3 = [0, 0, 0];
    for (const it of Object.values(items)) {
      if (it.pipeline !== scene.pipeline.name) continue;
      const parts = Object.values(it.parts);
      const n = parts.length || 1;
      for (let j = 0; j < n; j++) {
        const part = parts[j];
        itemPosition(layout, it, part?.lane, j, n, now, p);
        const st = part && it.finishedAt === undefined ? part.state : it.state;
        dots.push({ key: `${it.runId}:${j}`, id: it.runId, x: X(p), y: Y(p) - (p[1] - 0.55) * SCALE * 0.6, color: STATE_COLORS[st] });
      }
      if (dots.length > 1500) break;
    }
  }

  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={height ?? "100%"} role="img" aria-label={`Конвейер ${scene.pipeline.name}`} data-testid="conveyor-2d" style={{ display: "block" }} onClick={() => onSelectModule?.(null)}>
      <defs>
        <filter id="mf-glow" x="-50%" y="-50%" width="200%" height="200%">
          <feGaussianBlur stdDeviation="2.5" result="b" />
          <feMerge>
            <feMergeNode in="b" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>
      <polyline points={pts} fill="none" stroke="#1e293b" strokeWidth={SCALE * 1.6} strokeLinejoin="round" strokeLinecap="round" />
      <polyline points={pts} fill="none" stroke="#38bdf8" strokeOpacity={0.5} strokeWidth={1.5} strokeDasharray="6 8" className={animate ? "mf-belt-dash" : undefined} />
      {scene.routes.map((r) => {
        const a = layout.stages.find((s) => s.name === r.from);
        const b = layout.stages.find((s) => s.name === r.to);
        if (!a || !b) return null;
        const y0 = Y(a.center) - SCALE * 2.2;
        return <path key={`${r.from}>${r.to}`} d={`M${X(a.center)},${y0} Q${(X(a.center) + X(b.center)) / 2},${y0 - SCALE * 4} ${X(b.center)},${y0}`} fill="none" stroke="#fb923c" strokeWidth={2} strokeDasharray="5 4" />;
      })}
      {layout.stages.map((st) => {
        const m = scene.stages[st.index]!;
        const maxZ = Math.max(...st.lanes.map((l) => l.pos[2]), st.center[2]);
        return (
          <g key={st.index}>
            <text x={X(st.center)} y={Y([0, 0, maxZ]) + SCALE * 2.3} textAnchor="middle" fontSize={11} fill="#94a3b8">
              {st.index + 1} · {st.name}
              {m.kind === "parallel" ? ` ∥${m.lanes.length}` : m.kind === "foreach" ? " ⋔" : ""}
            </text>
            {st.lanes.map((l, j) => {
              const lane = m.lanes[j]!;
              const sel = selectedModule === lane.module;
              return (
                <g
                  key={l.stepId}
                  transform={`translate(${X(l.pos)},${Y(l.pos)})`}
                  style={{ cursor: onSelectModule ? "pointer" : undefined }}
                  onClick={(e) => {
                    e.stopPropagation();
                    onSelectModule?.(lane.module);
                  }}
                  data-module={lane.module}
                >
                  <rect x={-SCALE * 1.25} y={-SCALE * 1.05} width={SCALE * 2.5} height={SCALE * 2.1} rx={5} fill="#111c33" stroke={sel ? "#38bdf8" : "#334155"} strokeWidth={sel ? 2.5 : 1} />
                  <rect x={-SCALE * 1.1} y={SCALE * 0.75} width={SCALE * 2.2} height={3} rx={1.5} fill={HEALTH_COLORS[lane.health]} filter="url(#mf-glow)" />
                  <circle cx={SCALE * 0.95} cy={-SCALE * 0.75} r={3.5} fill={HEALTH_COLORS[lane.health]} className={lane.health !== "up" ? "mf-pulse" : undefined} />
                  <text y={-2} textAnchor="middle" fontSize={9.5} fill="#e2e8f0" fontWeight={600}>
                    {lane.module.length > 16 ? `${lane.module.slice(0, 15)}…` : lane.module}
                  </text>
                  <text y={10} textAnchor="middle" fontSize={8.5} fill="#94a3b8">
                    {lane.replicas.ready}/{lane.replicas.desired} · {lane.live.rate.toFixed(1)}/s{lane.live.queueDepth ? ` · q${lane.live.queueDepth}` : ""}
                  </text>
                </g>
              );
            })}
          </g>
        );
      })}
      <circle cx={X(layout.entry.pos)} cy={Y(layout.entry.pos)} r={7} fill="none" stroke="#38bdf8" strokeWidth={2} />
      <circle cx={X(layout.exit.pos)} cy={Y(layout.exit.pos)} r={7} fill="none" stroke="#4ade80" strokeWidth={2} />
      {dots.map((d) => (
        <circle
          key={d.key}
          cx={d.x}
          cy={d.y}
          r={4}
          fill={d.color}
          filter="url(#mf-glow)"
          style={{ cursor: onSelectRun ? "pointer" : undefined }}
          onClick={(e) => {
            e.stopPropagation();
            onSelectRun?.(d.id);
          }}
        />
      ))}
    </svg>
  );
}
