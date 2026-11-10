import { Html, RoundedBox } from "@react-three/drei";
import { useFrame, type ThreeEvent } from "@react-three/fiber";
import { memo, useLayoutEffect, useMemo, useRef, useState } from "react";
import * as THREE from "three";

import { useLive } from "../live/store";
import { gearGeometry } from "./gear";
import type { Vec3 } from "./layout";
import { HEALTH_COLORS, moduleHue, type LaneModel } from "./model";

const MAX_REPLICA_DOTS = 12;
const W = 2.5;
const D = 2.1;

export type ModuleBlockProps = {
  lane: LaneModel;
  pos: Vec3;
  /** Delay before the docking animation starts, ms (staggers blocks on first load). */
  dockDelay: number;
  selected: boolean;
  dimmed: boolean;
  variant?: "station" | "source";
  onSelect: (module: string) => void;
};

function formatRate(r: number): string {
  return r >= 10 ? r.toFixed(0) : r.toFixed(1);
}

/**
 * A module as a station plugged into the belt: a portal the items pass through, a status lamp,
 * a gear spinning with throughput, a queue column and one indicator per replica.
 * New blocks drop in from above with a damped spring ("docking").
 */
export const ModuleBlock = memo(function ModuleBlock({ lane, pos, dockDelay, selected, dimmed, variant = "station", onSelect }: ModuleBlockProps) {
  const group = useRef<THREE.Group>(null);
  const gear = useRef<THREE.Mesh>(null);
  const lamp = useRef<THREE.MeshStandardMaterial>(null);
  const strip = useRef<THREE.MeshBasicMaterial>(null);
  const body = useRef<THREE.MeshStandardMaterial>(null);
  const queue = useRef<THREE.Mesh>(null);
  const dots = useRef<THREE.InstancedMesh>(null);
  const [hover, setHover] = useState(false);
  const born = useRef<number | null>(null);
  const docked = lane.replicas.ready > 0 || lane.health !== "down";
  const hue = moduleHue(lane.module);
  const baseColor = useMemo(() => new THREE.Color().setHSL(hue, 0.35, 0.42), [hue]);
  const healthColor = useMemo(() => new THREE.Color(HEALTH_COLORS[lane.health]), [lane.health]);
  const errFlash = useMemo(() => new THREE.Color("#ff2d55"), []);
  const tmpColor = useMemo(() => new THREE.Color(), []);
  const capacity = Math.max(1, lane.replicas.ready);
  const load = Math.min(1, lane.live.inFlight / (capacity * 2));

  useLayoutEffect(() => {
    const m = dots.current;
    if (!m) return;
    const n = Math.min(MAX_REPLICA_DOTS, Math.max(lane.replicas.desired, lane.replicas.ready));
    const o = new THREE.Object3D();
    const c = new THREE.Color();
    for (let i = 0; i < MAX_REPLICA_DOTS; i++) {
      if (i < n) {
        const row = Math.floor(i / 6);
        const col = i % 6;
        o.position.set(-0.75 + col * 0.3, 0, D / 2 + 0.02 - 0.0 * row);
        o.position.y = -0.38 + row * -0.22;
        o.scale.setScalar(1);
      } else o.scale.setScalar(0);
      o.updateMatrix();
      m.setMatrixAt(i, o.matrix);
      c.set(i < lane.replicas.ready ? "#4ade80" : "#475569");
      if (i < lane.replicas.ready) c.multiplyScalar(2.2);
      m.setColorAt(i, c);
    }
    m.instanceMatrix.needsUpdate = true;
    if (m.instanceColor) m.instanceColor.needsUpdate = true;
  }, [lane.replicas.desired, lane.replicas.ready]);

  useFrame((state, dt) => {
    const now = performance.now();
    const t = state.clock.elapsedTime;
    if (docked && born.current === null) born.current = now + dockDelay;
    const g = group.current;
    if (g) {
      // Docking: critically under-damped spring from 7 units above.
      let y = 7;
      if (born.current !== null) {
        const k = Math.max(0, (now - born.current) / 1000);
        y = k <= 0 ? 7 : 7 * Math.exp(-5.5 * k) * Math.cos(7 * k);
      }
      g.position.y = y;
      const s = THREE.MathUtils.damp(g.scale.x, hover || selected ? 1.06 : 1, 10, dt);
      g.scale.setScalar(s);
    }
    if (gear.current) gear.current.rotation.z -= dt * (0.15 + Math.min(lane.live.rate, 20) * 0.6) * (lane.health === "down" ? 0 : 1);
    const lastErr = useLive.getState().lastErrorAt[lane.module] ?? 0;
    const flash = Math.max(0, 1 - (Date.now() - lastErr) / 1200);
    const pulse = lane.health === "up" ? 1 : 0.55 + 0.45 * Math.sin(t * (lane.health === "down" ? 9 : 5));
    if (lamp.current) {
      tmpColor.copy(healthColor).lerp(errFlash, flash);
      lamp.current.emissive.copy(tmpColor);
      lamp.current.emissiveIntensity = 2.2 * pulse + flash * 3;
      lamp.current.color.copy(tmpColor);
    }
    if (strip.current) {
      tmpColor.copy(healthColor).lerp(errFlash, flash).multiplyScalar(1.4 * pulse + flash * 2);
      strip.current.color.copy(tmpColor);
    }
    if (body.current) {
      body.current.emissiveIntensity = THREE.MathUtils.damp(body.current.emissiveIntensity, 0.08 + load * 0.55 + flash * 0.5, 6, dt);
      body.current.opacity = THREE.MathUtils.damp(body.current.opacity, dimmed ? 0.35 : lane.health === "down" ? 0.55 : 1, 6, dt);
    }
    if (queue.current) {
      const h = Math.min(3.2, Math.log2(1 + lane.live.queueDepth) * 0.6);
      queue.current.scale.y = THREE.MathUtils.damp(queue.current.scale.y, Math.max(0.001, h), 6, dt);
      queue.current.position.y = 1.0 + queue.current.scale.y / 2;
    }
  });

  const click = (e: ThreeEvent<MouseEvent>) => {
    e.stopPropagation();
    onSelect(lane.module);
  };

  const isSource = variant === "source";
  return (
    <group position={pos}>
      <group
        ref={group}
        position={[0, 7, 0]}
        onClick={click}
        onPointerOver={(e) => {
          e.stopPropagation();
          setHover(true);
          document.body.style.cursor = "pointer";
        }}
        onPointerOut={() => {
          setHover(false);
          document.body.style.cursor = "";
        }}
      >
        {/* portal legs */}
        {!isSource &&
          [-1, 1].map((side) => (
            <mesh key={side} castShadow position={[0, 0.75, side * (D / 2 - 0.12)]}>
              <boxGeometry args={[W * 0.9, 1.5, 0.22]} />
              <meshStandardMaterial color="#1f2937" metalness={0.85} roughness={0.3} />
            </mesh>
          ))}
        <RoundedBox args={isSource ? [W, 1.6, D] : [W, 1.05, D]} radius={0.12} smoothness={3} castShadow receiveShadow position={[0, isSource ? 1.0 : 1.95, 0]}>
          <meshStandardMaterial ref={body} color={baseColor} emissive={baseColor} emissiveIntensity={0.1} metalness={0.55} roughness={0.32} transparent opacity={1} />
        </RoundedBox>
        {/* status strip on the front face */}
        <mesh position={[0, isSource ? 0.35 : 1.5, D / 2 + 0.005]}>
          <planeGeometry args={[W * 0.86, 0.07]} />
          <meshBasicMaterial ref={strip} toneMapped={false} />
        </mesh>
        {/* gear */}
        <mesh ref={gear} geometry={gearGeometry()} position={[W / 2 - 0.55, isSource ? 1.15 : 2.0, D / 2 + 0.06]} castShadow>
          <meshStandardMaterial color="#cbd5e1" metalness={0.95} roughness={0.2} />
        </mesh>
        {/* replica indicators */}
        <group position={[0, isSource ? 1.35 : 2.4, 0]}>
          <instancedMesh ref={dots} args={[undefined, undefined, MAX_REPLICA_DOTS]}>
            <boxGeometry args={[0.18, 0.12, 0.06]} />
            <meshBasicMaterial toneMapped={false} />
          </instancedMesh>
        </group>
        {/* status lamp */}
        <mesh position={[-W / 2 + 0.35, isSource ? 1.95 : 2.62, D / 2 - 0.35]}>
          <sphereGeometry args={[0.17, 20, 20]} />
          <meshStandardMaterial ref={lamp} toneMapped={false} roughness={0.2} />
        </mesh>
        {/* queue column */}
        <mesh ref={queue} position={[W / 2 + 0.3, 1.0, -D / 2 + 0.3]} scale={[1, 0.001, 1]}>
          <cylinderGeometry args={[0.13, 0.13, 1, 12]} />
          <meshStandardMaterial color="#f59e0b" emissive="#f59e0b" emissiveIntensity={1.4} toneMapped={false} />
        </mesh>
        {selected && (
          <mesh position={[0, 0.02, 0]} rotation={[-Math.PI / 2, 0, 0]}>
            <ringGeometry args={[1.75, 1.95, 48]} />
            <meshBasicMaterial color={[0.4, 1.4, 2.4]} toneMapped={false} />
          </mesh>
        )}
        <Html position={[0, isSource ? 2.6 : 3.3, 0]} center distanceFactor={16} zIndexRange={[20, 0]} style={{ pointerEvents: "none" }}>
          <div className={`mf-label mf-label-${lane.health}${selected ? " mf-label-selected" : ""}`}>
            <div className="mf-label-name">{lane.module}</div>
            <div className="mf-label-meta">
              {lane.replicas.ready}/{lane.replicas.desired} · {formatRate(lane.live.rate)}/s
              {lane.live.queueDepth > 0 ? ` · q${lane.live.queueDepth}` : ""}
            </div>
          </div>
        </Html>
      </group>
    </group>
  );
});
