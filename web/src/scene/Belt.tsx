import { useFrame } from "@react-three/fiber";
import { useLayoutEffect, useMemo, useRef } from "react";
import * as THREE from "three";

import { directionAt, pointAt, type Layout, type Vec3 } from "./layout";

const SLAT_GAP = 0.62;
const BELT_W = 1.7;

type Segment = { mid: Vec3; len: number; yaw: number };

function segments(path: Vec3[]): Segment[] {
  const out: Segment[] = [];
  for (let i = 1; i < path.length; i++) {
    const a = path[i - 1]!;
    const b = path[i]!;
    const dx = b[0] - a[0];
    const dz = b[2] - a[2];
    const len = Math.hypot(dx, dz);
    if (len < 1e-3) continue;
    out.push({ mid: [(a[0] + b[0]) / 2, 0, (a[2] + b[2]) / 2], len, yaw: Math.atan2(-dz, dx) });
  }
  return out;
}

/** The belt: rubber bed, side rails, moving slats (one InstancedMesh) and lane manifolds under parallel stages. */
export function Belt({ layout, speed }: { layout: Layout; speed: number }) {
  const segs = useMemo(() => segments(layout.path), [layout]);
  const slats = useRef<THREE.InstancedMesh>(null);
  const count = Math.max(1, Math.floor(layout.length / SLAT_GAP));
  const offset = useRef(0);
  const tmp = useMemo(() => new THREE.Object3D(), []);
  const p = useMemo<Vec3>(() => [0, 0, 0], []);

  useFrame((_, dt) => {
    const m = slats.current;
    if (!m) return;
    offset.current = (offset.current + Math.min(dt, 0.1) * speed) % SLAT_GAP;
    for (let i = 0; i < count; i++) {
      const s = i * SLAT_GAP + offset.current;
      pointAt(layout, s, p);
      const d = directionAt(layout, s);
      tmp.position.set(p[0], 0.27, p[2]);
      tmp.rotation.set(0, Math.atan2(-d[2], d[0]), 0);
      tmp.updateMatrix();
      m.setMatrixAt(i, tmp.matrix);
    }
    m.instanceMatrix.needsUpdate = true;
  });

  const manifolds = layout.stages.filter((s) => s.lanes.length > 1);

  return (
    <group>
      {segs.map((s, i) => (
        <group key={i} position={s.mid} rotation={[0, s.yaw, 0]}>
          <mesh receiveShadow position={[0, 0.12, 0]}>
            <boxGeometry args={[s.len + BELT_W * 0.98, 0.24, BELT_W]} />
            <meshStandardMaterial color="#111827" roughness={0.85} metalness={0.15} />
          </mesh>
          {[-1, 1].map((side) => (
            <mesh key={side} castShadow position={[0, 0.3, side * (BELT_W / 2 + 0.06)]}>
              <boxGeometry args={[s.len + BELT_W, 0.16, 0.12]} />
              <meshStandardMaterial color="#94a3b8" metalness={0.9} roughness={0.25} />
            </mesh>
          ))}
          {[-1, 1].map((side) => (
            <mesh key={`g${side}`} position={[0, 0.39, side * (BELT_W / 2 + 0.06)]}>
              <boxGeometry args={[s.len + BELT_W, 0.02, 0.03]} />
              <meshBasicMaterial color={[0.25, 0.9, 1.6]} toneMapped={false} />
            </mesh>
          ))}
        </group>
      ))}
      {manifolds.map((st) => {
        const zs = st.lanes.map((l) => l.pos[2]);
        const span = Math.max(...zs) - Math.min(...zs);
        return (
          <group key={st.index} position={st.center}>
            <mesh receiveShadow position={[0, 0.11, 0]}>
              <boxGeometry args={[BELT_W * 1.6, 0.22, span + BELT_W]} />
              <meshStandardMaterial color="#0f172a" roughness={0.8} metalness={0.2} />
            </mesh>
            {st.lanes.map((l) => (
              <mesh key={l.stepId} receiveShadow position={[0, 0.2, l.pos[2] - st.center[2]]}>
                <boxGeometry args={[STAGE_LANE_LEN, 0.1, BELT_W * 0.8]} />
                <meshStandardMaterial color="#1e293b" roughness={0.6} metalness={0.4} />
              </mesh>
            ))}
          </group>
        );
      })}
      <instancedMesh ref={slats} args={[undefined, undefined, count]} receiveShadow>
        <boxGeometry args={[0.08, 0.04, BELT_W * 0.92]} />
        <meshStandardMaterial color="#334155" metalness={0.6} roughness={0.4} />
      </instancedMesh>
      <SlatInit mesh={slats} />
    </group>
  );
}

const STAGE_LANE_LEN = 4.2;

/** Hides slats until the first frame positions them (avoids a flash at the origin). */
function SlatInit({ mesh }: { mesh: React.RefObject<THREE.InstancedMesh | null> }) {
  useLayoutEffect(() => {
    const m = mesh.current;
    if (!m) return;
    const zero = new THREE.Matrix4().makeScale(0, 0, 0);
    for (let i = 0; i < m.count; i++) m.setMatrixAt(i, zero);
    m.instanceMatrix.needsUpdate = true;
  }, [mesh]);
  return null;
}
