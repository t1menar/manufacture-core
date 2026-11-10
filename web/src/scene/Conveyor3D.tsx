import { AdaptiveDpr, CameraControls, Environment, Grid, Html, Lightformer, PerformanceMonitor, QuadraticBezierLine } from "@react-three/drei";
import { Canvas, useFrame } from "@react-three/fiber";
import { Bloom, EffectComposer, SMAA, ToneMapping, Vignette } from "@react-three/postprocessing";
import { ToneMappingMode } from "postprocessing";
import { useEffect, useMemo, useRef, useState } from "react";
import * as THREE from "three";

import { Belt } from "./Belt";
import { computeLayout, LANE_GAP, type Layout, type Vec3 } from "./layout";
import type { ConveyorScene } from "./model";
import { ModuleBlock } from "./ModuleBlock";
import { RunItems } from "./RunItems";

export type ConveyorProps = {
  scene: ConveyorScene;
  selectedModule?: string | null;
  onSelectModule: (module: string | null) => void;
  onSelectRun: (runId: string) => void;
  /** Force post-processing off (tests, screenshots of the fallback path). */
  quality?: "high" | "low";
  onStats?: (s: { fps: number; items: number; total: number; effects: boolean }) => void;
};

function CameraRig({ layout, focus }: { layout: Layout; focus: Vec3 | null }) {
  const ref = useRef<CameraControls>(null);
  const key = `${layout.bounds.min.join()}|${layout.bounds.max.join()}`;
  useEffect(() => {
    const c = ref.current;
    if (!c) return;
    const { min, max } = layout.bounds;
    const cx = (min[0] + max[0]) / 2;
    const cz = (min[2] + max[2]) / 2;
    const span = Math.max(max[0] - min[0], (max[2] - min[2]) * 1.6, 14);
    if (focus) {
      void c.setLookAt(focus[0] + 5, 7.5, focus[2] + 10, focus[0], 1.2, focus[2], true);
    } else {
      void c.setLookAt(cx + span * 0.06, span * 0.5, max[2] + span * 0.78, cx, 0, cz, true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `key` captures the bounds
  }, [key, focus?.[0], focus?.[2]]);
  return <CameraControls ref={ref} makeDefault smoothTime={0.6} minDistance={4} maxDistance={260} maxPolarAngle={Math.PI * 0.47} dollyToCursor />;
}

function FpsMeter({ onFps }: { onFps: (fps: number) => void }) {
  const frames = useRef(0);
  const last = useRef(0);
  useFrame(() => {
    frames.current++;
    const now = performance.now();
    if (last.current === 0) last.current = now;
    if (now - last.current >= 1000) {
      onFps(Math.round((frames.current * 1000) / (now - last.current)));
      frames.current = 0;
      last.current = now;
    }
  });
  return null;
}

function RouteArc({ from, to }: { from: Vec3; to: Vec3 }) {
  const ref = useRef<{ material: { dashOffset: number } } | null>(null);
  useFrame((_, dt) => {
    if (ref.current) ref.current.material.dashOffset -= dt * 1.5;
  });
  const mid: Vec3 = [(from[0] + to[0]) / 2, 6 + Math.abs(from[0] - to[0]) * 0.25, (from[2] + to[2]) / 2 - 1.5];
  return (
    <QuadraticBezierLine
      ref={ref as never}
      start={[from[0], 3.2, from[2] - 0.6]}
      end={[to[0], 3.2, to[2] - 0.6]}
      mid={mid}
      color="#fb923c"
      lineWidth={2.2}
      dashed
      dashScale={2}
      dashSize={0.6}
      gapSize={0.4}
      transparent
      opacity={0.85}
    />
  );
}

function StageLabels({ layout, scene }: { layout: Layout; scene: ConveyorScene }) {
  return (
    <>
      {layout.stages.map((st) => {
        const m = scene.stages[st.index]!;
        const maxZ = Math.max(...st.lanes.map((l) => l.pos[2]), st.center[2]);
        return (
          <Html key={st.index} position={[st.center[0], 0.05, maxZ + LANE_GAP * 0.62]} center distanceFactor={18} zIndexRange={[5, 0]} style={{ pointerEvents: "none" }}>
            <div className="mf-stage-label">
              <span className="mf-stage-index">{st.index + 1}</span> {st.name}
              {m.kind !== "single" && <span className="mf-stage-kind">{m.kind === "parallel" ? `∥ ${m.lanes.length}` : "⋔ foreach"}</span>}
            </div>
          </Html>
        );
      })}
    </>
  );
}

function Dock({ pos, color, label }: { pos: Vec3; color: string; label: string }) {
  return (
    <group position={pos}>
      <mesh position={[0, 0.06, 0]} receiveShadow>
        <boxGeometry args={[1.6, 0.12, 2.4]} />
        <meshStandardMaterial color="#0b1220" metalness={0.4} roughness={0.5} />
      </mesh>
      <mesh position={[0, 0.14, 0]} rotation={[-Math.PI / 2, 0, 0]}>
        <ringGeometry args={[0.55, 0.68, 40]} />
        <meshBasicMaterial color={new THREE.Color(color).multiplyScalar(2)} toneMapped={false} />
      </mesh>
      <Html position={[0, 0.2, 1.7]} center distanceFactor={18} style={{ pointerEvents: "none" }}>
        <div className="mf-stage-label mf-dock-label">{label}</div>
      </Html>
    </group>
  );
}

function RejectBin({ pos }: { pos: Vec3 }) {
  return (
    <group position={pos}>
      <mesh receiveShadow>
        <boxGeometry args={[6, 0.2, 2.2]} />
        <meshStandardMaterial color="#1a0b12" metalness={0.3} roughness={0.7} />
      </mesh>
      {[-1, 1].map((s) => (
        <mesh key={s} position={[0, 0.22, s * 1.1]}>
          <boxGeometry args={[6, 0.25, 0.06]} />
          <meshBasicMaterial color={[1.6, 0.18, 0.3]} toneMapped={false} />
        </mesh>
      ))}
    </group>
  );
}

function World({ scene, layout, selectedModule, onSelectModule, onSelectRun, onItems }: ConveyorProps & { layout: Layout; onItems: (n: number, total: number) => void }) {
  const lanes = useMemo(() => {
    const out: { key: string; lane: ConveyorScene["stages"][number]["lanes"][number]; pos: Vec3; delay: number }[] = [];
    let k = 0;
    for (const st of layout.stages) {
      const m = scene.stages[st.index];
      if (!m) continue;
      st.lanes.forEach((l, j) => {
        const lane = m.lanes[j];
        if (lane) out.push({ key: `${st.index}:${l.stepId}`, lane, pos: l.pos, delay: (k++ % 24) * 110 });
      });
    }
    return out;
  }, [layout, scene.stages]);
  const routes = useMemo(
    () =>
      scene.routes.flatMap((r) => {
        const a = layout.stages.find((s) => s.name === r.from);
        const b = layout.stages.find((s) => s.name === r.to);
        return a && b ? [{ key: `${r.from}>${r.to}`, from: a.center, to: b.center }] : [];
      }),
    [scene.routes, layout],
  );
  const busy = scene.stages.reduce((acc, s) => acc + s.lanes.reduce((a, l) => a + l.live.rate, 0), 0);
  const sourcePos: Vec3 = [layout.entry.pos[0] - 1.6, 0, layout.entry.pos[2]];

  return (
    <>
      <Belt layout={layout} speed={0.6 + Math.min(busy, 20) * 0.08} />
      {lanes.map((l) => (
        <ModuleBlock
          key={l.key}
          lane={l.lane}
          pos={l.pos}
          dockDelay={l.delay}
          selected={selectedModule === l.lane.module}
          dimmed={Boolean(selectedModule) && selectedModule !== l.lane.module}
          onSelect={onSelectModule}
        />
      ))}
      {scene.source ? (
        <ModuleBlock
          lane={scene.source}
          pos={sourcePos}
          dockDelay={0}
          variant="source"
          selected={selectedModule === scene.source.module}
          dimmed={Boolean(selectedModule) && selectedModule !== scene.source.module}
          onSelect={onSelectModule}
        />
      ) : (
        <Dock pos={layout.entry.pos} color="#38bdf8" label="вход" />
      )}
      <Dock pos={layout.exit.pos} color="#4ade80" label="готово" />
      <RejectBin pos={layout.reject} />
      {routes.map((r) => (
        <RouteArc key={r.key} from={r.from} to={r.to} />
      ))}
      <StageLabels layout={layout} scene={scene} />
      <RunItems layout={layout} pipeline={scene.pipeline.name} onSelect={onSelectRun} onCount={onItems} />
    </>
  );
}

/** The real-time 3D conveyor (main screen). */
export function Conveyor3D(props: ConveyorProps) {
  const { scene, selectedModule, quality = "high", onStats } = props;
  const layout = useMemo(() => computeLayout(scene), [scene]);
  const [effects, setEffects] = useState(quality === "high");
  const stats = useRef({ fps: 0, items: 0, total: 0 });
  const focus = useMemo<Vec3 | null>(() => {
    if (!selectedModule) return null;
    for (const st of layout.stages) for (const l of st.lanes) if (l.module === selectedModule) return l.pos;
    return null;
  }, [selectedModule, layout]);
  const report = () => onStats?.({ ...stats.current, effects });

  return (
    <Canvas
      shadows
      dpr={[1, 2]}
      gl={{ antialias: false, powerPreference: "high-performance", preserveDrawingBuffer: true }}
      camera={{ fov: 38, near: 0.1, far: 600, position: [10, 20, 30] }}
      onPointerMissed={() => props.onSelectModule(null)}
      data-testid="conveyor-3d"
    >
      <color attach="background" args={["#060a13"]} />
      <fog attach="fog" args={["#060a13", 60, 220]} />
      <hemisphereLight args={["#9cc9ff", "#0b0f1a", 0.55]} />
      <directionalLight
        position={[layout.bounds.max[0] + 10, 26, layout.bounds.max[2] + 14]}
        intensity={2.1}
        castShadow
        shadow-mapSize={[2048, 2048]}
        shadow-camera-left={-80}
        shadow-camera-right={80}
        shadow-camera-top={80}
        shadow-camera-bottom={-80}
        shadow-bias={-0.0004}
      />
      <pointLight position={[layout.bounds.min[0], 8, layout.bounds.min[2] - 4]} intensity={60} color="#7c3aed" distance={60} />
      <Environment resolution={256} frames={1}>
        <Lightformer form="rect" intensity={2.4} position={[0, 12, 0]} rotation-x={Math.PI / 2} scale={[40, 8, 1]} color="#dbeafe" />
        <Lightformer form="rect" intensity={1.2} position={[-20, 4, 10]} rotation-y={Math.PI / 2} scale={[30, 4, 1]} color="#22d3ee" />
        <Lightformer form="rect" intensity={1.0} position={[20, 4, -10]} rotation-y={-Math.PI / 2} scale={[30, 4, 1]} color="#a78bfa" />
      </Environment>
      <Grid
        position={[0, -0.01, 0]}
        args={[400, 400]}
        cellSize={1}
        cellThickness={0.6}
        cellColor="#14213a"
        sectionSize={6.5}
        sectionThickness={1.1}
        sectionColor="#1e3a5f"
        fadeDistance={160}
        fadeStrength={1.4}
        infiniteGrid
      />
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -0.02, 0]} receiveShadow>
        <planeGeometry args={[600, 600]} />
        <shadowMaterial opacity={0.45} />
      </mesh>
      <World
        {...props}
        layout={layout}
        onItems={(n, total) => {
          stats.current.items = n;
          stats.current.total = total;
          report();
        }}
      />
      <CameraRig layout={layout} focus={focus} />
      <FpsMeter
        onFps={(fps) => {
          stats.current.fps = fps;
          report();
        }}
      />
      <PerformanceMonitor onDecline={() => setEffects(false)} onIncline={() => quality === "high" && setEffects(true)} flipflops={3} onFallback={() => setEffects(false)} />
      <AdaptiveDpr pixelated />
      {effects && (
        <EffectComposer multisampling={0} enableNormalPass={false}>
          <Bloom mipmapBlur luminanceThreshold={0.95} luminanceSmoothing={0.2} intensity={1.15} radius={0.72} />
          <ToneMapping mode={ToneMappingMode.AGX} />
          <Vignette eskil={false} offset={0.22} darkness={0.72} />
          <SMAA />
        </EffectComposer>
      )}
    </Canvas>
  );
}
