import { useFrame, type ThreeEvent } from "@react-three/fiber";
import { useMemo, useRef } from "react";
import * as THREE from "three";
import { RoundedBoxGeometry } from "three/addons/geometries/RoundedBoxGeometry.js";

import { useLive } from "../live/store";
import { MAX_ITEMS } from "../live/reduce";
import { itemPosition, type Layout, type Vec3 } from "./layout";
import { STATE_COLORS } from "./model";

const GLOW: Record<string, number> = { processing: 2.4, failed: 2.0, attention: 2.0, done: 1.4, moving: 1.0, queued: 0.6 };

/**
 * All runs on the belt as one InstancedMesh (one instance per item part). Positions and colors
 * are computed every frame straight from the live store, so SSE bursts never re-render React.
 */
export function RunItems({ layout, pipeline, onSelect, onCount }: { layout: Layout; pipeline: string; onSelect: (runId: string) => void; onCount?: (shown: number, total: number) => void }) {
  const mesh = useRef<THREE.InstancedMesh>(null);
  const ids = useRef<string[]>([]);
  const geom = useMemo(() => new RoundedBoxGeometry(0.5, 0.42, 0.5, 2, 0.07), []);
  const mat = useMemo(() => {
    const m = new THREE.MeshStandardMaterial({ metalness: 0.2, roughness: 0.35 });
    m.onBeforeCompile = (shader) => {
      shader.fragmentShader = shader.fragmentShader.replace(
        "#include <emissivemap_fragment>",
        "#include <emissivemap_fragment>\n#ifdef USE_COLOR\n totalEmissiveRadiance += max(vColor.rgb - 0.7, 0.0) * 0.9 + vColor.rgb * 0.12;\n#endif",
      );
    };
    return m;
  }, []);
  const tmp = useMemo(() => new THREE.Object3D(), []);
  const color = useMemo(() => new THREE.Color(), []);
  const p = useMemo<Vec3>(() => [0, 0, 0], []);
  const lastReport = useRef(0);

  useFrame((state) => {
    const m = mesh.current;
    if (!m) return;
    const now = Date.now();
    const t = state.clock.elapsedTime;
    const items = useLive.getState().items;
    let n = 0;
    let total = 0;
    const list = ids.current;
    list.length = 0;
    for (const id in items) {
      const it = items[id]!;
      if (it.pipeline !== pipeline) continue;
      total++;
      const parts = Object.values(it.parts);
      const count = parts.length || 1;
      for (let j = 0; j < count && n < MAX_ITEMS; j++) {
        const part = parts[j];
        const st = part && it.finishedAt === undefined ? part.state : it.state;
        itemPosition(layout, it, part?.lane, j, count, now, p);
        tmp.position.set(p[0], p[1], p[2]);
        const phase = (id.charCodeAt(0) + j) * 0.7;
        if (st === "processing") {
          tmp.position.y += 0.08 * Math.sin(t * 6 + phase);
          tmp.rotation.set(0, t * 2.2 + phase, 0);
        } else if (st === "failed" && it.finishedAt !== undefined) {
          tmp.rotation.set(t * 3, 0, t * 2);
        } else if (st === "attention") {
          tmp.rotation.set(0, t * 0.8, 0.15 * Math.sin(t * 3));
        } else tmp.rotation.set(0, 0, 0);
        const sc = it.finishedAt !== undefined ? Math.max(0.05, 1 - Math.max(0, now - it.finishedAt - 1500) / 1000) : 1;
        tmp.scale.setScalar(count > 1 ? sc * 0.72 : sc);
        tmp.updateMatrix();
        m.setMatrixAt(n, tmp.matrix);
        color.set(STATE_COLORS[st]).multiplyScalar(GLOW[st] ?? 1);
        if (st === "attention" || (st === "failed" && it.finishedAt === undefined)) color.multiplyScalar(0.8 + 0.4 * Math.sin(t * 8 + phase));
        m.setColorAt(n, color);
        list.push(id);
        n++;
      }
    }
    m.count = n;
    m.instanceMatrix.needsUpdate = true;
    if (m.instanceColor) m.instanceColor.needsUpdate = true;
    if (onCount && now - lastReport.current > 500) {
      lastReport.current = now;
      onCount(n, total);
    }
  });

  const click = (e: ThreeEvent<MouseEvent>) => {
    if (e.instanceId === undefined) return;
    const id = ids.current[e.instanceId];
    if (id) {
      e.stopPropagation();
      onSelect(id);
    }
  };

  return <instancedMesh ref={mesh} args={[geom, mat, MAX_ITEMS]} castShadow onClick={click} frustumCulled={false} />;
}
